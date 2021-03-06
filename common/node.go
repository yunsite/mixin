package common

import (
	"fmt"

	"github.com/MixinNetwork/mixin/crypto"
)

const (
	NodeStatePledging  = "PLEDGING"
	NodeStateAccepted  = "ACCEPTED"
	NodeStateDeparting = "DEPARTING"
)

type Node struct {
	Signer Address
	Payee  Address
	State  string
}

func (n *Node) IsAccepted() bool {
	return n.State == NodeStateAccepted
}

func (tx *Transaction) validateNodePledge(store DataStore, inputs map[string]*UTXO) error {
	if len(tx.Outputs) != 1 {
		return fmt.Errorf("invalid outputs count %d for pledge transaction", len(tx.Outputs))
	}
	if len(tx.Extra) != 2*len(crypto.Key{}) {
		return fmt.Errorf("invalid extra length %d for pledge transaction", len(tx.Extra))
	}
	for _, in := range inputs {
		if in.Type != OutputTypeScript {
			return fmt.Errorf("invalid utxo type %d", in.Type)
		}
	}

	o := tx.Outputs[0]
	if o.Amount.Cmp(NewInteger(10000)) != 0 {
		return fmt.Errorf("invalid pledge amount %s", o.Amount.String())
	}
	for _, n := range store.ReadConsensusNodes() {
		if n.State != NodeStateAccepted {
			return fmt.Errorf("invalid node pending state %s %s", n.Signer.String(), n.State)
		}
	}

	return nil
}

func (tx *Transaction) validateNodeAccept(store DataStore) error {
	if len(tx.Outputs) != 1 {
		return fmt.Errorf("invalid outputs count %d for accept transaction", len(tx.Outputs))
	}
	if len(tx.Inputs) != 2 {
		return fmt.Errorf("invalid inputs count %d for accept transaction", len(tx.Inputs))
	}
	var pledging *Node
	filter := make(map[string]string)
	nodes := store.ReadConsensusNodes()
	for _, n := range nodes {
		filter[n.Signer.String()] = n.State
		if n.State == NodeStateDeparting {
			return fmt.Errorf("invalid node pending state %s %s", n.Signer.String(), n.State)
		}
		if n.State == NodeStateAccepted {
			continue
		}
		if n.State == NodeStatePledging && pledging == nil {
			pledging = n
		} else {
			return fmt.Errorf("invalid pledging nodes %s %s", pledging.Signer.String(), n.Signer.String())
		}
	}
	if pledging == nil {
		return fmt.Errorf("no pledging node needs to get accepted")
	}

	lastAccept, err := store.ReadTransaction(tx.Inputs[0].Hash)
	if err != nil {
		return err
	}
	ao := lastAccept.Outputs[0]
	if len(lastAccept.Outputs) != 1 {
		return fmt.Errorf("invalid accept utxo count %d", len(lastAccept.Outputs))
	}
	if ao.Type != OutputTypeNodeAccept {
		return fmt.Errorf("invalid accept utxo type %d", ao.Type)
	}
	var publicSpend crypto.Key
	copy(publicSpend[:], lastAccept.Extra)
	privateView := publicSpend.DeterministicHashDerive()
	acc := Address{
		PublicViewKey:  privateView.Public(),
		PublicSpendKey: publicSpend,
	}
	if filter[acc.String()] != NodeStateAccepted {
		return fmt.Errorf("invalid accept utxo source %s", filter[acc.String()])
	}

	lastPledge, err := store.ReadTransaction(tx.Inputs[1].Hash)
	if err != nil {
		return err
	}
	po := lastPledge.Outputs[0]
	if len(lastPledge.Outputs) != 1 {
		return fmt.Errorf("invalid pledge utxo count %d", len(lastPledge.Outputs))
	}
	if po.Type != OutputTypeNodePledge {
		return fmt.Errorf("invalid pledge utxo type %d", po.Type)
	}
	copy(publicSpend[:], lastPledge.Extra)
	privateView = publicSpend.DeterministicHashDerive()
	acc = Address{
		PublicViewKey:  privateView.Public(),
		PublicSpendKey: publicSpend,
	}
	if filter[acc.String()] != NodeStatePledging {
		return fmt.Errorf("invalid pledge utxo source %s", filter[acc.String()])
	}

	nodesAmount := NewInteger(uint64(10000 * len(nodes)))
	if ao.Amount.Add(po.Amount).Cmp(nodesAmount) != 0 {
		return fmt.Errorf("invalid accept input amount %s %s %s", ao.Amount, po.Amount, nodesAmount)
	}
	return nil
}
