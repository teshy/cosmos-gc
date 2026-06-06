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
	// trailing block makes the node replay it on restart, which panics on this chain's
	// BeginBlocker ("validator does not exist") because the GC'd state lacks the validator
	// records the replay needs. Capping to the committed height makes store == state == app on
	// restart → no replay → clean boot. (Realio fork fix, 2026-06-06.)
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
	var (
		hKey          []byte = []byte("H:" + fmt.Sprint(latestHeight))
		hVal          []byte
		cKey          []byte = []byte("C:" + fmt.Sprint(latestHeight-1))
		cVal          []byte
		pKey          []byte = []byte("P:" + fmt.Sprint(latestHeight) + ":0")
		pVal          []byte
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
	pVal, err = dbOld.Get(pKey)
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
	batch.Set(pKey, pVal)
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
