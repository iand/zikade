package zikade

import (
	"bytes"
	"context"
	"testing"

	"github.com/ipfs/go-libdht/kad/key/bitstr"

	"github.com/iand/xorbie/keystore"
	"github.com/probe-lab/zikade/kadt"
)

var _ keystore.Keystore[kadt.Key] = (*providerKeystore)(nil)

func TestProviderKeystorePersistsAcrossReload(t *testing.T) {
	ctx := context.Background()

	dstore, err := InMemoryDatastore()
	if err != nil {
		t.Fatalf("datastore: %v", err)
	}

	k := kadt.NewKey([]byte("multihash-bytes"))

	ks1, err := newProviderKeystore(ctx, dstore)
	if err != nil {
		t.Fatalf("new keystore: %v", err)
	}
	if err := ks1.Add(ctx, k); err != nil {
		t.Fatalf("add: %v", err)
	}

	ks2, err := newProviderKeystore(ctx, dstore)
	if err != nil {
		t.Fatalf("reload keystore: %v", err)
	}
	if !keystoreContains(ks2, k) {
		t.Error("reloaded keystore missing the persisted key")
	}

	if err := ks1.Remove(ctx, k); err != nil {
		t.Fatalf("remove: %v", err)
	}

	ks3, err := newProviderKeystore(ctx, dstore)
	if err != nil {
		t.Fatalf("reload keystore: %v", err)
	}
	if keystoreContains(ks3, k) {
		t.Error("removed key still present after reload")
	}
}

func keystoreContains(ks *providerKeystore, k kadt.Key) bool {
	for got := range ks.KeysUnder(bitstr.Key("")) {
		if bytes.Equal(got.MsgKey(), k.MsgKey()) {
			return true
		}
	}
	return false
}
