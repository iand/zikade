package zikade

import (
	"context"
	"fmt"
	"iter"

	dsq "github.com/ipfs/go-datastore/query"
	"github.com/ipfs/go-libdht/kad/key/bitstr"

	"github.com/iand/xorbie/keystore"
	"github.com/probe-lab/zikade/kadt"
)

const namespaceProvideSet = "provideset"

type providerKeystore struct {
	dstore Datastore
	index  *keystore.Trie[kadt.Key]
}

func newProviderKeystore(ctx context.Context, dstore Datastore) (*providerKeystore, error) {
	ks := &providerKeystore{
		dstore: dstore,
		index:  keystore.New[kadt.Key](),
	}

	q, err := dstore.Query(ctx, dsq.Query{Prefix: "/" + namespaceProvideSet})
	if err != nil {
		return nil, fmt.Errorf("query provide set: %w", err)
	}
	defer q.Close()

	for e := range q.Next() {
		if e.Error != nil {
			return nil, fmt.Errorf("read provide set entry: %w", e.Error)
		}
		ks.index.Add(kadt.NewKey(e.Value))
	}

	return ks, nil
}

func (ks *providerKeystore) Add(ctx context.Context, k kadt.Key) error {
	if err := ks.dstore.Put(ctx, newDatastoreKey(namespaceProvideSet, string(k.MsgKey())), k.MsgKey()); err != nil {
		return fmt.Errorf("put provide set entry: %w", err)
	}
	ks.index.Add(k)
	return nil
}

func (ks *providerKeystore) Remove(ctx context.Context, k kadt.Key) error {
	if err := ks.dstore.Delete(ctx, newDatastoreKey(namespaceProvideSet, string(k.MsgKey()))); err != nil {
		return fmt.Errorf("delete provide set entry: %w", err)
	}
	ks.index.Remove(k)
	return nil
}

func (ks *providerKeystore) KeysUnder(prefix bitstr.Key) iter.Seq[kadt.Key] {
	return ks.index.KeysUnder(prefix)
}
