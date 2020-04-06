package dht

import (
	"fmt"

	"github.com/libp2p/go-libp2p-core/event"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"

	"github.com/libp2p/go-eventbus"

	ma "github.com/multiformats/go-multiaddr"

	"github.com/jbenet/goprocess"
)

// subscriberNotifee implements network.Notifee and also manages the subscriber to the event bus. We consume peer
// identification events to trigger inclusion in the routing table, and we consume Disconnected events to eject peers
// from it.
type subscriberNotifee struct {
	dht  *IpfsDHT
	subs event.Subscription
}

func newSubscriberNotifiee(dht *IpfsDHT) (*subscriberNotifee, error) {
	bufSize := eventbus.BufSize(256)

	evts := []interface{}{
		// register for event bus notifications of when peers successfully complete identification in order to update
		// the routing table
		new(event.EvtPeerIdentificationCompleted),

		// register for event bus protocol ID changes in order to update the routing table
		new(event.EvtPeerProtocolsUpdated),

		// register for event bus notifications for when our local address/addresses change so we can
		// advertise those to the network
		new(event.EvtLocalAddressesUpdated),
	}

	// register for event bus local routability changes in order to trigger switching between client and server modes
	// only register for events if the DHT is operating in ModeAuto
	if dht.auto {
		evts = append(evts, new(event.EvtLocalReachabilityChanged))
	}

	subs, err := dht.host.EventBus().Subscribe(evts, bufSize)
	if err != nil {
		return nil, fmt.Errorf("dht could not subscribe to eventbus events; err: %s", err)
	}

	nn := &subscriberNotifee{
		dht:  dht,
		subs: subs,
	}

	// register for network notifications
	dht.host.Network().Notify(nn)

	// Fill routing table with currently connected peers that are DHT servers
	dht.plk.Lock()
	defer dht.plk.Unlock()
	for _, p := range dht.host.Network().Peers() {
		dht.peerFound(dht.ctx, p, false)
	}

	return nn, nil
}

func (nn *subscriberNotifee) subscribe(proc goprocess.Process) {
	dht := nn.dht
	defer dht.host.Network().StopNotify(nn)
	defer nn.subs.Close()

	for {
		select {
		case e, more := <-nn.subs.Out():
			if !more {
				return
			}

			switch evt := e.(type) {
			case event.EvtLocalAddressesUpdated:
				// when our address changes, we should proactively tell our closest peers about it so
				// we become discoverable quickly. The Identify protocol will push a signed peer record
				// with our new address to all peers we are connected to. However, we might not necessarily be connected
				// to our closet peers & so in the true spirit of Zen, searching for ourself in the network really is the best way
				// to to forge connections with those matter.
				select {
				case dht.triggerSelfLookup <- nil:
				default:
				}
			case event.EvtPeerProtocolsUpdated:
				handlePeerProtocolsUpdatedEvent(dht, evt)
			case event.EvtPeerIdentificationCompleted:
				handlePeerIdentificationCompletedEvent(dht, evt)
			case event.EvtLocalReachabilityChanged:
				if dht.auto {
					handleLocalReachabilityChangedEvent(dht, evt)
				} else {
					// something has gone really wrong if we get an event we did not subscribe to
					logger.Errorf("received LocalReachabilityChanged event that was not subscribed to")
				}
			default:
				// something has gone really wrong if we get an event for another type
				logger.Errorf("got wrong type from subscription: %T", e)
			}
		case <-proc.Closing():
			return
		}
	}
}

func handlePeerIdentificationCompletedEvent(dht *IpfsDHT, e event.EvtPeerIdentificationCompleted) {
	dht.plk.Lock()
	defer dht.plk.Unlock()
	if dht.host.Network().Connectedness(e.Peer) != network.Connected {
		return
	}

	// if the peer supports the DHT protocol, add it to our RT and kick a refresh if needed
	valid, err := dht.validRTPeer(e.Peer)
	if err != nil {
		logger.Errorf("could not check peerstore for protocol support: err: %s", err)
		return
	} else if valid {
		dht.peerFound(dht.ctx, e.Peer, false)
		dht.fixRTIfNeeded()
	}
}

func handlePeerProtocolsUpdatedEvent(dht *IpfsDHT, e event.EvtPeerProtocolsUpdated) {
	valid, err := dht.validRTPeer(e.Peer)
	if err != nil {
		logger.Errorf("could not check peerstore for protocol support: err: %s", err)
		return
	}

	if !valid {
		dht.peerStoppedDHT(dht.ctx, e.Peer)
		return
	}

	// we just might have discovered a peer that supports the DHT protocol
	dht.fixRTIfNeeded()
}

func handleLocalReachabilityChangedEvent(dht *IpfsDHT, e event.EvtLocalReachabilityChanged) {
	var target mode

	switch e.Reachability {
	case network.ReachabilityPrivate, network.ReachabilityUnknown:
		target = modeClient
	case network.ReachabilityPublic:
		target = modeServer
	}

	logger.Infof("processed event %T; performing dht mode switch", e)

	err := dht.setMode(target)
	// NOTE: the mode will be printed out as a decimal.
	if err == nil {
		logger.Infow("switched DHT mode successfully", "mode", target)
	} else {
		logger.Errorw("switching DHT mode failed", "mode", target, "error", err)
	}
}

// validRTPeer returns true if the peer supports the DHT protocol and false otherwise. Supporting the DHT protocol means
// supporting the primary protocols, we do not want to add peers that are speaking obsolete secondary protocols to our
// routing table
func (dht *IpfsDHT) validRTPeer(p peer.ID) (bool, error) {
	protos, err := dht.peerstore.SupportsProtocols(p, protocol.ConvertToStrings(dht.protocols)...)
	if err != nil {
		return false, err
	}

	return len(protos) > 0, nil
}

func (nn *subscriberNotifee) Disconnected(n network.Network, v network.Conn) {
	dht := nn.dht
	select {
	case <-dht.Process().Closing():
		return
	default:
	}

	p := v.RemotePeer()

	// Lock and check to see if we're still connected. We lock to make sure
	// we don't concurrently process a connect event.
	dht.plk.Lock()
	defer dht.plk.Unlock()
	if dht.host.Network().Connectedness(p) == network.Connected {
		// We're still connected.
		return
	}

	dht.smlk.Lock()
	defer dht.smlk.Unlock()
	ms, ok := dht.strmap[p]
	if !ok {
		return
	}
	delete(dht.strmap, p)

	// Do this asynchronously as ms.lk can block for a while.
	go func() {
		if err := ms.lk.Lock(dht.Context()); err != nil {
			return
		}
		defer ms.lk.Unlock()
		ms.invalidate()
	}()
}

func (nn *subscriberNotifee) Connected(n network.Network, v network.Conn)      {}
func (nn *subscriberNotifee) OpenedStream(n network.Network, v network.Stream) {}
func (nn *subscriberNotifee) ClosedStream(n network.Network, v network.Stream) {}
func (nn *subscriberNotifee) Listen(n network.Network, a ma.Multiaddr)         {}
func (nn *subscriberNotifee) ListenClose(n network.Network, a ma.Multiaddr)    {}