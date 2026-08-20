package cplutil

import (
	"testing"

	"github.com/ipfs/go-libdht/kad/key/bitstr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/kadt"
)

func TestGenRandKeyForPrefix(t *testing.T) {
	prefixes := []bitstr.Key{
		"",
		"0",
		"1",
		"01",
		"0101",
		"11110000",
		"1010101010101010",
	}

	for _, p := range prefixes {
		t.Run(string(p), func(t *testing.T) {
			k, err := GenRandKeyForPrefix(p)
			require.NoError(t, err)

			// the minted key's leading bits equal the prefix
			for i := 0; i < len(p); i++ {
				want := uint(0)
				if p[i] == '1' {
					want = 1
				}
				assert.Equalf(t, want, k.Bit(i), "bit %d of key %s", i, k.HexString())
			}

			// the wire preimage hashes back to the same key, so the key round-trips
			assert.Equal(t, 256, k.CommonPrefixLength(kadt.NewKey(k.MsgKey())))
		})
	}
}

func TestGenRandKeyForPrefixTooLong(t *testing.T) {
	_, err := GenRandKeyForPrefix(bitstr.Key("00000000000000000")) // 17 bits
	require.Error(t, err)
}
