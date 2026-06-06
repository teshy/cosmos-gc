package pruner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cdb "github.com/cometbft/cometbft-db"
	cmtstore "github.com/cometbft/cometbft/proto/tendermint/store"
	"github.com/cometbft/cometbft/store"
)

func PruneBlockstoreDB(dataDir string, committedHeight int64) error {
	// Open old db (if it exists)
	if _, err := os.Stat(filepath.Join(dataDir, "blockstore.db")); os.IsNotExist(err) {
		return fmt.Errorf("blockstore.db does not exist in %s", dataDir)
	}
	dbOld, err := cdb.NewGoLevelDB("blockstore", dataDir)
	if err != nil {
		return err
	}

	// Get latest height, CAPPED to the committed app/state height. The blockstore can sit one
	// block AHEAD of the committed app state (the normal transient at shutdown). Keeping that
	// trailing block makes the node replay it on restart, which panics on chains whose
	// BeginBlocker validates against pruned state (e.g. a multistaking module checking validator
	// records: "validator does not exist"), and leaves store > state on chains that don't panic.
	// Capping to the committed height makes store == state == app on restart → no replay → clean boot.
	blockStore := store.NewBlockStore(dbOld)
	latestHeight := blockStore.Height()
	if committedHeight > 0 && committedHeight < latestHeight {
		latestHeight = committedHeight
	}

	// Get blockhash of latest height
	meta := blockStore.LoadBlockMeta(latestHeight)
	latestHash := strings.ToLower(meta.BlockID.Hash.String())

	// Create new db and populate latest info
	if err := os.RemoveAll(filepath.Join(dataDir, "blockstore.new.db")); err != nil {
		return err
	}
	dbNew, err := cdb.NewGoLevelDB("blockstore.new", dataDir)
	if err != nil {
		return err
	}
	numParts := int(meta.BlockID.PartSetHeader.Total)
	if numParts == 0 {
		numParts = 1 // always copy at least part 0
	}

	var (
		hKey          []byte = []byte("H:" + fmt.Sprint(latestHeight))
		hVal          []byte
		cKey          []byte = []byte("C:" + fmt.Sprint(latestHeight-1))
		cVal          []byte
		scKey         []byte = []byte("SC:" + fmt.Sprint(latestHeight))
		scVal         []byte
		bhKey         []byte = []byte("BH:" + latestHash)
		bhVal         []byte
		blockstoreKey []byte = []byte("blockStore")
		blockstoreVal []byte
	)
	hVal, err = dbOld.Get(hKey)
	if err != nil {
		return err
	}
	cVal, err = dbOld.Get(cKey)
	if err != nil {
		return err
	}
	scVal, err = dbOld.Get(scKey)
	if err != nil {
		return err
	}
	bhVal, err = dbOld.Get(bhKey)
	if err != nil {
		return err
	}
	// Write a FRESH BlockStoreState meta reflecting the (possibly capped) single retained height,
	// instead of copying the old meta. Copying the old meta would report the pre-cap height and
	// re-introduce the "store ahead of state" replay. With base == height == latestHeight the
	// store reports exactly the one retained block; the node blocksyncs forward from there.
	bss := cmtstore.BlockStoreState{Base: latestHeight, Height: latestHeight}
	blockstoreVal, err = bss.Marshal()
	if err != nil {
		return err
	}
	batch := dbNew.NewBatch()
	batch.Set(hKey, hVal)
	batch.Set(cKey, cVal)
	// Copy all block parts — blocks with many transactions have more than one part.
	// The original code hardcoded P:N:0 and silently dropped parts 1+ on large blocks.
	for i := 0; i < numParts; i++ {
		pKey := []byte(fmt.Sprintf("P:%d:%d", latestHeight, i))
		pVal, err := dbOld.Get(pKey)
		if err != nil {
			return err
		}
		if pVal == nil {
			// numParts comes from the block meta's PartSetHeader.Total, so every index in
			// [0,numParts) must exist in the source DB. A nil here means the source is missing a
			// declared part (corruption, or a prior buggy GC) — fail loudly rather than silently
			// write an incomplete, unreadable retained block, which is the very failure this fix
			// exists to prevent.
			return fmt.Errorf("block %d: part %d of %d is missing from the source blockstore — refusing to write an incomplete block", latestHeight, i, numParts)
		}
		batch.Set(pKey, pVal)
	}
	batch.Set(scKey, scVal)
	batch.Set(bhKey, bhVal)
	batch.Set(blockstoreKey, blockstoreVal)
	if err := batch.WriteSync(); err != nil {
		return err
	}
	if err := batch.Close(); err != nil {
		return err
	}

	// Remove old db and rename new db
	if err := dbOld.Close(); err != nil {
		return err
	}
	if err := dbNew.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "blockstore.db")); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dataDir, "blockstore.new.db"), filepath.Join(dataDir, "blockstore.db")); err != nil {
		return err
	}

	return nil
}
