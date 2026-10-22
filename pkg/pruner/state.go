package pruner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	cdb "github.com/cometbft/cometbft-db"
	cmtstate "github.com/cometbft/cometbft/proto/tendermint/state"
	"github.com/cometbft/cometbft/state"
)

func PruneStateDB(dataDir string) (retErr error) {
	// Open old db (if it exists)
	if _, err := os.Stat(filepath.Join(dataDir, "state.db")); os.IsNotExist(err) {
		return fmt.Errorf("state.db does not exist in %s", dataDir)
	}
	dbOld, err := cdb.NewGoLevelDB("state", dataDir)
	if err != nil {
		return err
	}

	// On any error return, close what was opened and remove the half-written new DB, so the source
	// stays unlocked and untouched. The success path below closes both itself; the closed flags keep
	// this from closing twice. Once the old DB is about to be removed (swapping) the new DB may be
	// the only complete copy, so it is left in place from there on. The original error is returned
	// (joined with any cleanup error).
	var (
		dbNew     *cdb.GoLevelDB
		closedOld bool
		closedNew bool
		swapping  bool
	)
	defer func() {
		if retErr == nil {
			return
		}
		errs := []error{retErr}
		if !closedOld {
			if err := dbOld.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if dbNew != nil {
			if !closedNew {
				if err := dbNew.Close(); err != nil {
					errs = append(errs, err)
				}
			}
			if !swapping {
				if err := os.RemoveAll(filepath.Join(dataDir, "state.new.db")); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) > 1 {
			retErr = errors.Join(errs...)
		}
	}()

	// Get the important heights
	storeOld := state.NewStore(dbOld, state.StoreOptions{})
	storeStateOld, err := storeOld.Load()
	if err != nil {
		return err
	}
	latestHeight := storeStateOld.LastBlockHeight
	lastValHeight := storeStateOld.LastHeightValidatorsChanged
	if lastValHeight > latestHeight {
		// If lastValHeight is greater than latestHeight, then we need to find the lastValHeight
		// by looking at the validators info at latestHeight-1
		bytes, err := dbOld.Get([]byte("validatorsKey:" + fmt.Sprint(latestHeight-1)))
		if err != nil {
			return err
		}
		if len(bytes) > 0 {
			valInfo := new(cmtstate.ValidatorsInfo)
			if err := valInfo.Unmarshal(bytes); err != nil {
				return err
			}
			lastValHeight = valInfo.LastHeightChanged
		}
	}

	// Create new db and populate latest info
	if err := os.RemoveAll(filepath.Join(dataDir, "state.new.db")); err != nil {
		return err
	}
	dbNew, err = cdb.NewGoLevelDB("state.new", dataDir)
	if err != nil {
		return err
	}
	storeNew := state.NewStore(dbNew, state.StoreOptions{})
	if err := storeNew.Bootstrap(storeStateOld); err != nil {
		return err
	}
	valKey := []byte("validatorsKey:" + fmt.Sprint(lastValHeight))
	valBytes, err := dbOld.Get(valKey)
	if err != nil {
		return err
	}
	if err := dbNew.SetSync(valKey, valBytes); err != nil {
		return err
	}

	// Remove old db and rename new db
	closedOld = true
	if err := dbOld.Close(); err != nil {
		return err
	}
	closedNew = true
	if err := dbNew.Close(); err != nil {
		return err
	}
	swapping = true
	if err := os.RemoveAll(filepath.Join(dataDir, "state.db")); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dataDir, "state.new.db"), filepath.Join(dataDir, "state.db")); err != nil {
		return err
	}

	return nil
}

// GetCommittedHeight returns state.db's LastBlockHeight (the committed consensus/app height).
// Used to cap the blockstore prune so it never retains a block ahead of the committed app state.
// Call BEFORE the concurrent pruners run — it opens state.db read-only and closes it, avoiding a
// goleveldb lock conflict with PruneStateDB.
func GetCommittedHeight(dataDir string) (int64, error) {
	db, err := cdb.NewGoLevelDB("state", dataDir)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	st, err := state.NewStore(db, state.StoreOptions{}).Load()
	if err != nil {
		return 0, err
	}
	return st.LastBlockHeight, nil
}
