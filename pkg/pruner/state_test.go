package pruner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cdb "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/types"
)

// newStateHome writes a minimal valid State (height 5, one validator) into a goleveldb "state"
// under a temp dir through CometBFT's own store, and returns the data dir.
const lastHeight int64 = 5

func newStateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := cdb.NewGoLevelDB("state", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	val := types.NewValidator(ed25519.GenPrivKeyFromSecret([]byte("validator")).PubKey(), 10)
	vals := types.NewValidatorSet([]*types.Validator{val})
	params := types.DefaultConsensusParams()
	st := state.State{
		ChainID:                          "test",
		InitialHeight:                    1,
		Validators:                       vals,
		NextValidators:                   vals,
		LastValidators:                   vals,
		LastHeightValidatorsChanged:      1,
		ConsensusParams:                  *params,
		LastHeightConsensusParamsChanged: 1,
	}
	store := state.NewStore(db, state.StoreOptions{})
	// Save at the initial height writes the validator set under validatorsKey:1, which is what
	// LastHeightValidatorsChanged points at once the chain has moved on.
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	st.LastBlockHeight = lastHeight
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertStateCleanedUp is the shared check after a failed PruneStateDB: the source is still
// there, unlocked (it reopens in this process) and no half-written new DB is left beside it.
func assertStateCleanedUp(t *testing.T, dir string, wantHeight int64) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "state.db")); err != nil {
		t.Fatalf("source state.db is gone after the failed prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.new.db")); !os.IsNotExist(err) {
		t.Errorf("state.new.db is left behind after the failed prune (stat err=%v)", err)
	}
	h, err := GetCommittedHeight(dir)
	if err != nil {
		t.Fatalf("source state DB cannot be reopened after the failed prune: %v", err)
	}
	if h != wantHeight {
		t.Errorf("source LastBlockHeight = %d, want %d", h, wantHeight)
	}
}

func TestPruneStateDB_Success(t *testing.T) {
	dir := newStateHome(t)
	if err := PruneStateDB(dir); err != nil {
		t.Fatalf("PruneStateDB: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.new.db")); !os.IsNotExist(err) {
		t.Errorf("state.new.db still exists after a successful prune (stat err=%v)", err)
	}
	h, err := GetCommittedHeight(dir)
	if err != nil || h != lastHeight {
		t.Errorf("pruned state reopens with LastBlockHeight %d (err=%v), want %d", h, err, lastHeight)
	}
}

// newRawStateHome returns a data dir holding a goleveldb "state" with the given raw keys, no
// State saved through the store.
func newRawStateHome(t *testing.T, kv map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	db, err := cdb.NewGoLevelDB("state", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for k, v := range kv {
		if err := db.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// LastHeightValidatorsChanged is ahead of the committed height, so PruneStateDB reads
// validatorsKey:<height-1>, which here is garbage: the error returns after state.db is open and
// before state.new.db exists.
func TestPruneStateDB_BadValidatorsInfoCleansUp(t *testing.T) {
	dir := t.TempDir()
	db, err := cdb.NewGoLevelDB("state", dir)
	if err != nil {
		t.Fatal(err)
	}
	val := types.NewValidator(ed25519.GenPrivKeyFromSecret([]byte("validator")).PubKey(), 10)
	vals := types.NewValidatorSet([]*types.Validator{val})
	st := state.State{
		ChainID:                          "test",
		InitialHeight:                    1,
		LastBlockHeight:                  lastHeight,
		Validators:                       vals,
		NextValidators:                   vals,
		LastValidators:                   vals,
		LastHeightValidatorsChanged:      lastHeight + 1,
		ConsensusParams:                  *types.DefaultConsensusParams(),
		LastHeightConsensusParamsChanged: 1,
	}
	if err := state.NewStore(db, state.StoreOptions{}).Save(st); err != nil {
		t.Fatal(err)
	}
	if err := db.Set([]byte(fmt.Sprintf("validatorsKey:%d", lastHeight-1)), []byte("not a protobuf ValidatorsInfo")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := PruneStateDB(dir); err == nil {
		t.Fatal("PruneStateDB returned nil for an undecodable validators record")
	}
	assertStateCleanedUp(t, dir, lastHeight)
}

// An empty state.db loads as an empty State, so Bootstrap or the validators-record copy fails
// ("value cannot be nil"): either way the error returns after state.new.db is open and
// half-written.
func TestPruneStateDB_BootstrapErrorCleansUp(t *testing.T) {
	dir := newRawStateHome(t, nil)
	err := PruneStateDB(dir)
	if err == nil {
		t.Fatal("PruneStateDB returned nil for an empty state.db")
	}
	t.Logf("error: %v", err)
	assertStateCleanedUp(t, dir, 0)
}
