package pruner

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cosmossdk.io/log"
	storeiavl "cosmossdk.io/store/iavl"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	cdb "github.com/cosmos/cosmos-db"
	"github.com/cosmos/iavl"
)

func PruneApplicationDB(dataDir string) (retErr error) {
	// Open old db (if it exists)
	if _, err := os.Stat(filepath.Join(dataDir, "application.db")); os.IsNotExist(err) {
		return fmt.Errorf("application.db does not exist in %s", dataDir)
	}
	cdbOld, err := cdb.NewGoLevelDB("application", dataDir, nil)
	if err != nil {
		return err
	}

	// On any error return, close what was opened and remove the half-written new DB, so the source
	// stays unlocked and untouched. The success path below closes both itself; the closed flags keep
	// this from closing twice. Once the old DB is about to be removed (swapping) the new DB may be
	// the only complete copy, so it is left in place from there on. The original error is returned
	// (joined with any cleanup error).
	var (
		cdbNew    *cdb.GoLevelDB
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
			if err := cdbOld.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if cdbNew != nil {
			if !closedNew {
				if err := cdbNew.Close(); err != nil {
					errs = append(errs, err)
				}
			}
			if !swapping {
				if err := os.RemoveAll(filepath.Join(dataDir, "application.new.db")); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) > 1 {
			retErr = errors.Join(errs...)
		}
	}()

	// Get latest height
	latestHeight := rootmulti.GetLatestVersion(cdbOld)

	// Get all module keys
	storeOld := rootmulti.NewStore(cdbOld, log.NewNopLogger(), metrics.NewNoOpMetrics())
	commitInfo, err := storeOld.GetCommitInfo(latestHeight)
	if err != nil {
		return err
	}
	storeKeys := []*storetypes.KVStoreKey{}
	for _, info := range commitInfo.StoreInfos {
		// Skip stores that are not of type sdk.StoreTypeIAVL
		if strings.HasPrefix(info.Name, "mem_") {
			continue
		}
		storeKeys = append(storeKeys, storetypes.NewKVStoreKey(info.Name))
	}

	// Initialise old store
	for _, storeKey := range storeKeys {
		storeOld.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, nil)
	}
	if err := storeOld.LoadLatestVersion(); err != nil {
		return err
	}

	// Create new db and import latest version from old db
	if err := os.RemoveAll(filepath.Join(dataDir, "application.new.db")); err != nil {
		return err
	}
	cdbNew, err = cdb.NewGoLevelDB("application.new", dataDir, nil)
	if err != nil {
		return err
	}
	storeNew := rootmulti.NewStore(cdbNew, log.NewNopLogger(), metrics.NewNoOpMetrics())
	for _, storeKey := range storeKeys {
		storeNew.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, nil)
	}
	if err := storeNew.LoadLatestVersion(); err != nil {
		return err
	}
	for _, storeKey := range storeKeys {
		kvStoreOld := storeOld.GetCommitKVStore(storeKey)
		exp, err := kvStoreOld.(*storeiavl.Store).Export(latestHeight)
		if err != nil {
			// error will be thrown when tree root is null for a given height
			// we should set it back to empty bytes in the new store
			binaryHeight := make([]byte, 8)
			binary.BigEndian.PutUint64(binaryHeight, uint64(latestHeight))
			key := append([]byte("s/k:"+storeKey.Name()+"/r"), binaryHeight...)
			if err := cdbNew.Set(key, []byte{}); err != nil {
				return err
			}
			continue
		}
		kvStoreNew := storeNew.GetCommitKVStore(storeKey)
		inp, err := kvStoreNew.(*storeiavl.Store).Import(latestHeight)
		if err != nil {
			exp.Close()
			return err
		}
		for {
			node, err := exp.Next()
			if err == iavl.ErrorExportDone {
				break
			}
			if err := inp.Add(node); err != nil {
				inp.Close()
				exp.Close()
				return err
			}
		}
		if err := inp.Commit(); err != nil {
			inp.Close()
			exp.Close()
			return err
		}
	}

	// Copy latest height and commit info
	val, err := cdbOld.Get([]byte("s/latest"))
	if err != nil {
		return err
	}
	cdbNew.Set([]byte("s/latest"), val)
	val, err = commitInfo.Marshal()
	if err != nil {
		return err
	}
	cdbNew.Set([]byte("s/"+fmt.Sprint(latestHeight)), val)

	// Remove old db and rename new db
	closedOld = true
	if err := cdbOld.Close(); err != nil {
		return err
	}
	closedNew = true
	if err := cdbNew.Close(); err != nil {
		return err
	}
	swapping = true
	if err := os.RemoveAll(filepath.Join(dataDir, "application.db")); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dataDir, "application.new.db"), filepath.Join(dataDir, "application.db")); err != nil {
		return err
	}

	return nil
}
