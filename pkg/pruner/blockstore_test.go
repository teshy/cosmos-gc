package pruner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdb "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/crypto/tmhash"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
)

const testHeight int64 = 10

// newTestHome saves one real block at testHeight through the public BlockStore API into a
// goleveldb "blockstore" under a temp dir, so every key PruneBlockstoreDB reads (H, C, SC, BH,
// P:<h>:<i>) is the one CometBFT itself wrote. The part size is the protocol's own: LoadBlock
// rejects a non-final part of any other size, so a multi-part block is made by being larger than
// one part, not by shrinking the part. Returns the data dir and the number of parts saved.
func newTestHome(t *testing.T, numTxs, txSize int, partSize uint32) (string, int) {
	t.Helper()
	dir := t.TempDir()
	db, err := cdb.NewGoLevelDB("blockstore", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	txs := make([]types.Tx, numTxs)
	for i := range txs {
		txs[i] = []byte(fmt.Sprintf("%0*d", txSize, i))
	}
	block := types.MakeBlock(testHeight, txs, testCommit(testHeight-1), nil)
	block.ChainID = "test"
	block.Time = time.Unix(1, 0).UTC()
	block.ValidatorsHash = tmhash.Sum([]byte("validators"))
	block.ProposerAddress = tmhash.SumTruncated([]byte("proposer"))

	parts, err := block.MakePartSet(partSize)
	if err != nil {
		t.Fatal(err)
	}
	bs := store.NewBlockStore(db)
	bs.SaveBlock(block, parts, testCommit(testHeight))
	return dir, int(parts.Total())
}

// testCommit is a commit LoadBlock accepts: a non-zero BlockID and one signature.
func testCommit(height int64) *types.Commit {
	return &types.Commit{
		Height: height,
		BlockID: types.BlockID{
			Hash:          tmhash.Sum([]byte("block")),
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte("parts"))},
		},
		Signatures: []types.CommitSig{{
			BlockIDFlag:      types.BlockIDFlagCommit,
			ValidatorAddress: tmhash.SumTruncated([]byte("validator")),
			Timestamp:        time.Unix(1, 0).UTC(),
			Signature:        make([]byte, 64),
		}},
	}
}

func partKey(i int) []byte { return []byte(fmt.Sprintf("P:%d:%d", testHeight, i)) }

func TestPruneBlockstoreDB_AllParts(t *testing.T) {
	dir, total := newTestHome(t, 3, 30000, types.BlockPartSizeBytes)
	if total < 2 {
		t.Fatalf("fixture has %d part(s), want at least 2", total)
	}

	if err := PruneBlockstoreDB(dir, testHeight); err != nil {
		t.Fatalf("PruneBlockstoreDB: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "blockstore.new.db")); !os.IsNotExist(err) {
		t.Errorf("blockstore.new.db still exists after a successful prune (stat err=%v)", err)
	}
	db, err := cdb.NewGoLevelDB("blockstore", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < total; i++ {
		if ok, err := db.Has(partKey(i)); err != nil || !ok {
			t.Errorf("part %d of %d missing from the pruned blockstore (err=%v)", i, total, err)
		}
	}
	bs := store.NewBlockStore(db)
	if bs.Height() != testHeight || bs.Base() != testHeight {
		t.Errorf("pruned store base/height = %d/%d, want %d/%d", bs.Base(), bs.Height(), testHeight, testHeight)
	}
	if bs.LoadBlock(testHeight) == nil {
		t.Errorf("LoadBlock(%d) is nil after the prune", testHeight)
	}
}

func TestPruneBlockstoreDB_MissingPart(t *testing.T) {
	dir, total := newTestHome(t, 3, 30000, types.BlockPartSizeBytes)
	if total < 2 {
		t.Fatalf("fixture has %d part(s), want at least 2", total)
	}
	db, err := cdb.NewGoLevelDB("blockstore", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(partKey(1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	err = PruneBlockstoreDB(dir, testHeight)
	if err == nil {
		t.Fatal("PruneBlockstoreDB returned nil although part 1 is missing from the source")
	}
	if !strings.Contains(err.Error(), "part 1 of") || !strings.Contains(err.Error(), "missing") {
		t.Errorf("error %q does not name the missing part", err)
	}

	// The function must return before "Remove old db and rename new db", and must clean up on the
	// way out: the source is still there, unlocked (it reopens in this process) and still holds the
	// parts that were present, and no half-written new DB is left beside it.
	if _, err := os.Stat(filepath.Join(dir, "blockstore.db")); err != nil {
		t.Fatalf("source blockstore.db is gone after the failed prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blockstore.new.db")); !os.IsNotExist(err) {
		t.Errorf("blockstore.new.db is left behind after the failed prune (stat err=%v)", err)
	}
	src, err := cdb.NewGoLevelDB("blockstore", dir)
	if err != nil {
		t.Fatalf("source DB cannot be reopened after the failed prune: %v", err)
	}
	defer src.Close()
	if v, err := src.Get(partKey(0)); err != nil || v == nil {
		t.Errorf("P:%d:0 is no longer readable in the source (err=%v)", testHeight, err)
	}
	if v, err := src.Get(partKey(1)); err != nil || v != nil {
		t.Errorf("P:%d:1 should still be absent in the source (err=%v)", testHeight, err)
	}
}

func TestPruneBlockstoreDB_SinglePart(t *testing.T) {
	dir, total := newTestHome(t, 1, 10, types.BlockPartSizeBytes)
	if total != 1 {
		t.Fatalf("fixture has %d parts, want 1", total)
	}

	if err := PruneBlockstoreDB(dir, testHeight); err != nil {
		t.Fatalf("PruneBlockstoreDB: %v", err)
	}

	db, err := cdb.NewGoLevelDB("blockstore", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if bs := store.NewBlockStore(db); bs.LoadBlock(testHeight) == nil {
		t.Errorf("LoadBlock(%d) is nil after the prune", testHeight)
	}
}
