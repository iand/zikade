package cplutil

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/ipfs/go-libdht/kad/key/bitstr"
	mh "github.com/multiformats/go-multihash"

	"github.com/probe-lab/zikade/kadt"
)

// maxPrefixBits is the longest prefix [GenRandKeyForPrefix] can mint a key for, bounded by the
// width of the precomputed table.
const maxPrefixBits = 16

// GenRandKeyForPrefix returns a [kadt.Key] whose leading bits equal prefix. The key carries the
// preimage that the Amino RPCs put on the wire, so the receiving node can recover the Kademlia key
// by hashing it.
func GenRandKeyForPrefix(prefix bitstr.Key) (kadt.Key, error) {
	l := len(prefix)
	if l > maxPrefixBits {
		return kadt.Key{}, fmt.Errorf("cannot generate key for prefix longer than %d bits", maxPrefixBits)
	}

	// Build the 16-bit value the table is indexed by. The leading bits come from prefix, most
	// significant first, and the remaining low bits are random so successive calls for the same
	// prefix return different keys.
	var p uint16
	for i := range l {
		if prefix[i] == '1' {
			p |= 1 << (15 - i)
		}
	}
	if l < maxPrefixBits {
		var buf [2]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return kadt.Key{}, fmt.Errorf("read random bits: %w", err)
		}
		mask := uint16(0xFFFF) >> l
		p = (p &^ mask) | (binary.BigEndian.Uint16(buf[:]) & mask)
	}

	// keyPrefixMap[p] is a preimage whose SHA256 has p as its leading 16 bits, wrapped as the
	// sha2-256 multihash the table was generated from.
	id := [32 + 2]byte{mh.SHA2_256, 32}
	binary.BigEndian.PutUint32(id[2:], keyPrefixMap[p])
	return kadt.NewKey(id[:]), nil
}
