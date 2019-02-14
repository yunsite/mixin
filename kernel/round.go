package kernel

import (
	"encoding/binary"
	"fmt"

	"github.com/MixinNetwork/mixin/common"
	"github.com/MixinNetwork/mixin/crypto"
	"github.com/MixinNetwork/mixin/logger"
	"github.com/MixinNetwork/mixin/storage"
)

// each node has many different final hashes
// each broadcast should be accepted
//
// 1. never update the round if next round available with valid snapshots
// 2. whennever conflict snapshot accepted, update according to timestamp rules, never prune
// 3. 2 should follow 1 at first, e.g. if node A has an old snapshot in round n and has round n+1, an earlier conflict snapshot should never be accepted
// 4. all normal nodes should broadcast all snapshots in round order
// 5. expand rule 3, if node A has conflict snapshot in round n and node A round n has been referenced by other nodes, should never prune it
// 6. expand 5, earlier snapshot can be pruned if a conflict snapshot referenced by later rounds
// 7. all snapshots in a round should always have the same references, never prune snapshots, have transactions redundancy
// 8. if a snapshot passed the round gap, then requeue it to the transaction queues

type CacheRound struct {
	NodeId     crypto.Hash
	Number     uint64
	Start      uint64
	References [2]crypto.Hash
	End        uint64             `msgpack:"-"`
	Snapshots  []*common.Snapshot `msgpack:"-"`
}

type FinalRound struct {
	NodeId crypto.Hash
	Number uint64
	Start  uint64
	End    uint64
	Hash   crypto.Hash
}

type RoundGraph struct {
	Nodes      []crypto.Hash
	CacheRound map[crypto.Hash]*CacheRound
	FinalRound map[crypto.Hash]*FinalRound
	FinalCache []FinalRound
}

func (g *RoundGraph) UpdateFinalCache() {
	finals := make([]FinalRound, 0)
	for _, f := range g.FinalRound {
		finals = append(finals, FinalRound{
			NodeId: f.NodeId,
			Number: f.Number,
			Start:  f.Start,
		})
	}
	g.FinalCache = finals
}

func (g *RoundGraph) Print() string {
	desc := "ROUND GRAPH BEGIN\n"
	for _, id := range g.Nodes {
		desc = desc + fmt.Sprintf("NODE# %s\n", id)
		final := g.FinalRound[id]
		desc = desc + fmt.Sprintf("FINAL %d %d %s\n", final.Number, final.Start, final.Hash)
		cache := g.CacheRound[id]
		desc = desc + fmt.Sprintf("CACHE %d %d\n", cache.Number, cache.Start)
	}
	desc = desc + "ROUND GRAPH END"
	return desc
}

func LoadRoundGraph(store storage.Store, networkId crypto.Hash) (*RoundGraph, error) {
	graph := &RoundGraph{
		CacheRound: make(map[crypto.Hash]*CacheRound),
		FinalRound: make(map[crypto.Hash]*FinalRound),
	}

	consensusNodes := store.ReadConsensusNodes()
	for _, cn := range consensusNodes {
		id := cn.Account.Hash().ForNetwork(networkId)
		graph.Nodes = append(graph.Nodes, id)

		cache, err := loadHeadRoundForNode(store, id)
		if err != nil {
			return nil, err
		}
		if cache == nil {
			continue
		}
		graph.CacheRound[cache.NodeId] = cache

		finalRoundNumber := cache.Number - 1
		if cache.Number == 0 {
			finalRoundNumber = cache.Number
			graph.CacheRound[id] = &CacheRound{
				NodeId: id,
				Number: 1,
				Start:  0,
			}
		}
		final, err := loadFinalRoundForNode(store, id, finalRoundNumber)
		if err != nil {
			return nil, err
		}
		graph.FinalRound[final.NodeId] = final
	}

	logger.Println("\n" + graph.Print())
	graph.UpdateFinalCache()
	return graph, nil
}

func loadHeadRoundForNode(store storage.Store, nodeIdWithNetwork crypto.Hash) (*CacheRound, error) {
	meta, err := store.ReadRound(nodeIdWithNetwork)
	if err != nil || meta == nil {
		return nil, err
	}

	round := &CacheRound{
		NodeId:     nodeIdWithNetwork,
		Number:     meta.Number,
		Start:      meta.Timestamp,
		References: meta.References,
		End:        0,
	}
	round.Snapshots, err = store.ReadSnapshotsForNodeRound(round.NodeId, round.Number)
	if err != nil {
		return nil, err
	}
	for _, s := range round.Snapshots {
		if s.Timestamp < round.Start {
			panic(round.NodeId.String())
		}
		if s.Timestamp > round.End {
			round.End = s.Timestamp
		}
	}
	return round, nil
}

func loadFinalRoundForNode(store storage.Store, nodeIdWithNetwork crypto.Hash, number uint64) (*FinalRound, error) {
	snapshots, err := store.ReadSnapshotsForNodeRound(nodeIdWithNetwork, number)
	if err != nil {
		return nil, err
	}

	start := snapshots[0].Timestamp
	end := snapshots[len(snapshots)-1].Timestamp
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, number)
	hashes := append(nodeIdWithNetwork[:], buf...)
	for _, s := range snapshots {
		h := crypto.NewHash(s.Payload())
		hashes = append(hashes, h[:]...)
		if s.Timestamp < start {
			panic(*s)
		}
		if s.Timestamp > end {
			panic(*s)
		}
	}
	round := &FinalRound{
		NodeId: nodeIdWithNetwork,
		Number: number,
		Start:  start,
		End:    end,
		Hash:   crypto.NewHash(hashes),
	}
	return round, nil
}

func snapshotAsCacheRound(s *common.Snapshot) *CacheRound {
	return &CacheRound{
		NodeId:    s.NodeId,
		Number:    s.RoundNumber,
		Start:     s.Timestamp,
		End:       s.Timestamp,
		Snapshots: []*common.Snapshot{s},
	}
}

func (c *CacheRound) Copy() *CacheRound {
	r := *c
	r.Snapshots = append([]*common.Snapshot{}, c.Snapshots...)
	return &r
}

func (f *FinalRound) Copy() *FinalRound {
	r := *f
	return &r
}

func (c *CacheRound) asFinal() *FinalRound {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, c.Number)
	hash := crypto.NewHash(append(c.NodeId[:], buf...))
	start, end := c.Snapshots[0].Timestamp, uint64(0)
	for _, s := range c.Snapshots {
		hash = hash.ByteOr(s.PayloadHash())
		if s.Timestamp < start {
			start = s.Timestamp
		}
		if s.Timestamp > end {
			end = s.Timestamp
		}
	}
	round := &FinalRound{
		NodeId: c.NodeId,
		Number: c.Number,
		Start:  start,
		End:    end,
		Hash:   hash,
	}
	return round
}
