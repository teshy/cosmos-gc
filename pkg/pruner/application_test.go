package pruner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	cdb "github.com/cosmos/cosmos-db"
)

// newAppHome commits one version of a small multistore (one IAVL store, numKeys keys) through
// the SDK's own rootmulti store into a goleveldb "application" under a temp dir, and returns the
// data dir.
func newAppHome(t *testing.T, numKeys int) string {
	t.Helper()
	dir := t.TempDir()
	db, err := cdb.NewGoLevelDB("application", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := storetypes.NewKVStoreKey("bank")
	rs := rootmulti.NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	rs.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	if err := rs.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}
	kv := rs.GetCommitKVStore(key)
	for i := 0; i < numKeys; i++ {
		kv.Set([]byte(fmt.Sprintf("key-%03d", i)), []byte(fmt.Sprintf("value-%03d", i)))
	}
	rs.Commit()
	return dir
}

func openApp(t *testing.T, dir string) *cdb.GoLevelDB {
	t.Helper()
	db, err := cdb.NewGoLevelDB("application", dir, nil)
	if err != nil {
		t.Fatalf("application DB cannot be opened: %v", err)
	}
	return db
}

// assertAppCleanedUp is the shared check after a failed PruneApplicationDB: the source is still
// there, unlocked (it reopens in this process) and no half-written new DB is left beside it.
func assertAppCleanedUp(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "application.db")); err != nil {
		t.Fatalf("source application.db is gone after the failed prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "application.new.db")); !os.IsNotExist(err) {
		t.Errorf("application.new.db is left behind after the failed prune (stat err=%v)", err)
	}
	openApp(t, dir).Close()
}

func TestPruneApplicationDB_Success(t *testing.T) {
	dir := newAppHome(t, 20)
	if err := PruneApplicationDB(dir); err != nil {
		t.Fatalf("PruneApplicationDB: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "application.new.db")); !os.IsNotExist(err) {
		t.Errorf("application.new.db still exists after a successful prune (stat err=%v)", err)
	}

	db := openApp(t, dir)
	defer db.Close()
	rs := rootmulti.NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	key := storetypes.NewKVStoreKey("bank")
	rs.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	if err := rs.LoadLatestVersion(); err != nil {
		t.Fatalf("pruned application DB does not load: %v", err)
	}
	if v := rs.GetCommitKVStore(key).Get([]byte("key-007")); string(v) != "value-007" {
		t.Errorf("key-007 = %q after the prune, want value-007", v)
	}
}

// An empty application.db has no commit info at the latest version: the error returns after
// application.db is open and before application.new.db exists.
func TestPruneApplicationDB_NoCommitInfoCleansUp(t *testing.T) {
	dir := t.TempDir()
	openApp(t, dir).Close()

	if err := PruneApplicationDB(dir); err == nil {
		t.Fatal("PruneApplicationDB returned nil for an empty application.db")
	}
	assertAppCleanedUp(t, dir)
}

// IAVL nodes are stored as "s/k:bank/s" + version + nonce, and the root has nonce 1. deleteNode
// deletes the nth node key (0-based, in key order) of the committed "bank" tree in the source.
func deleteNode(t *testing.T, dir string, n int) {
	t.Helper()
	db := openApp(t, dir)
	defer db.Close()
	it, err := db.Iterator([]byte("s/k:bank/s"), []byte("s/k:bank/t"))
	if err != nil {
		t.Fatal(err)
	}
	var key []byte
	for i := 0; it.Valid(); it.Next() {
		if i == n {
			key = append([]byte(nil), it.Key()...)
			break
		}
		i++
	}
	it.Close()
	if key == nil {
		t.Fatalf("fixture has no IAVL node number %d", n)
	}
	if err := db.Delete(key); err != nil {
		t.Fatal(err)
	}
}

// The root node of the committed tree is missing, so the old store does not load: the error
// returns after application.db is open and before application.new.db exists.
func TestPruneApplicationDB_MissingRootCleansUp(t *testing.T) {
	dir := newAppHome(t, 20)
	deleteNode(t, dir, 0)

	err := PruneApplicationDB(dir)
	if err == nil {
		t.Fatal("PruneApplicationDB returned nil with the tree root missing from the source")
	}
	t.Logf("error: %v", err)
	assertAppCleanedUp(t, dir)
}

// A node deep in the tree is missing (node 30 of the 39 the 20-key fixture writes): the old store
// still loads, the export runs, and the import's Commit then rejects the incomplete tree. That error
// returns with application.new.db open and half-written, and the exporter and importer still open.
func TestPruneApplicationDB_MissingNodeCleansUp(t *testing.T) {
	dir := newAppHome(t, 20)
	deleteNode(t, dir, 30)

	err := PruneApplicationDB(dir)
	if err == nil {
		t.Fatal("PruneApplicationDB returned nil with a node missing from the source tree")
	}
	if !strings.Contains(err.Error(), "invalid node structure") {
		t.Fatalf("error %q is not the import commit failure, so this fixture no longer reaches the path it is meant to", err)
	}
	assertAppCleanedUp(t, dir)
}
