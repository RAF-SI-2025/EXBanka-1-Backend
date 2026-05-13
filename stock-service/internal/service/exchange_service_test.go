package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// fakeExchangeSeedRepo is a minimal in-memory ExchangeSeedRepo.
type fakeExchangeSeedRepo struct {
	exchanges  []model.StockExchange
	upsertErr  error
	upsertedBy []string // MIC codes upserted in order
	getByID    map[uint64]*model.StockExchange
}

func newFakeExchangeSeedRepo() *fakeExchangeSeedRepo {
	return &fakeExchangeSeedRepo{getByID: map[uint64]*model.StockExchange{}}
}

func (f *fakeExchangeSeedRepo) GetByID(id uint64) (*model.StockExchange, error) {
	if ex, ok := f.getByID[id]; ok {
		return ex, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeExchangeSeedRepo) GetByAcronym(acronym string) (*model.StockExchange, error) {
	for i := range f.exchanges {
		if f.exchanges[i].Acronym == acronym {
			return &f.exchanges[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeExchangeSeedRepo) List(_ string, _, _ int) ([]model.StockExchange, int64, error) {
	return f.exchanges, int64(len(f.exchanges)), nil
}
func (f *fakeExchangeSeedRepo) UpsertByMICCode(ex *model.StockExchange) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upsertedBy = append(f.upsertedBy, ex.MICCode)
	return nil
}

// fakeSettingRepo is a minimal in-memory SettingRepo.
type fakeSettingRepo struct {
	store  map[string]string
	getErr error
}

func newFakeSettingRepo() *fakeSettingRepo { return &fakeSettingRepo{store: map[string]string{}} }
func (f *fakeSettingRepo) Get(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	val, ok := f.store[key]
	if !ok {
		return "", gorm.ErrRecordNotFound
	}
	return val, nil
}
func (f *fakeSettingRepo) Set(key, value string) error {
	f.store[key] = value
	return nil
}

func TestExchangeService_GetExchange_Found(t *testing.T) {
	repo := newFakeExchangeSeedRepo()
	repo.getByID[7] = &model.StockExchange{ID: 7, Acronym: "NYSE"}
	svc := NewExchangeService(repo, newFakeSettingRepo())
	got, err := svc.GetExchange(7)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("got %d", got.ID)
	}
}

func TestExchangeService_GetExchange_NotFound(t *testing.T) {
	repo := newFakeExchangeSeedRepo()
	svc := NewExchangeService(repo, newFakeSettingRepo())
	_, err := svc.GetExchange(404)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrExchangeNotFound) {
		t.Errorf("expected ErrExchangeNotFound, got %v", err)
	}
}

func TestExchangeService_GetExchange_OtherErrorPropagates(t *testing.T) {
	// Use an underlying repo that returns a non-NotFound error.
	customErr := errors.New("db down")
	repo := &errExchangeSeedRepo{err: customErr}
	svc := NewExchangeService(repo, newFakeSettingRepo())
	_, err := svc.GetExchange(1)
	if !errors.Is(err, customErr) {
		t.Errorf("expected propagated error, got %v", err)
	}
}

func TestExchangeService_ListExchanges_Delegates(t *testing.T) {
	repo := newFakeExchangeSeedRepo()
	repo.exchanges = []model.StockExchange{{ID: 1, Acronym: "NYSE"}, {ID: 2, Acronym: "NASDAQ"}}
	svc := NewExchangeService(repo, newFakeSettingRepo())
	got, total, err := svc.ListExchanges("", 1, 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("got %d / %d", total, len(got))
	}
}

func TestExchangeService_SetTestingMode_True(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	if err := svc.SetTestingMode(true); err != nil {
		t.Fatalf("err: %v", err)
	}
	if settingRepo.store["testing_mode"] != "true" {
		t.Errorf("got %q", settingRepo.store["testing_mode"])
	}
}

func TestExchangeService_SetTestingMode_False(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	if err := svc.SetTestingMode(false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if settingRepo.store["testing_mode"] != "false" {
		t.Errorf("got %q", settingRepo.store["testing_mode"])
	}
}

func TestExchangeService_GetTestingMode_TrueValue(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	settingRepo.store["testing_mode"] = "true"
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	if !svc.GetTestingMode() {
		t.Error("expected true")
	}
}

func TestExchangeService_GetTestingMode_FalseDefault(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	settingRepo.store["testing_mode"] = "false"
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	if svc.GetTestingMode() {
		t.Error("expected false")
	}
}

func TestExchangeService_GetTestingMode_NotFoundDefaults(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	settingRepo.getErr = errors.New("oops")
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	if svc.GetTestingMode() {
		t.Error("expected false on error")
	}
}

func TestExchangeService_IsExchangeOpen_TestingMode(t *testing.T) {
	settingRepo := newFakeSettingRepo()
	settingRepo.store["testing_mode"] = "true"
	svc := NewExchangeService(newFakeExchangeSeedRepo(), settingRepo)
	open, err := svc.IsExchangeOpen(99)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !open {
		t.Error("testing mode should always be open")
	}
}

func TestExchangeService_IsExchangeOpen_NotFound(t *testing.T) {
	svc := NewExchangeService(newFakeExchangeSeedRepo(), newFakeSettingRepo())
	_, err := svc.IsExchangeOpen(404)
	if err == nil {
		t.Fatal("expected error for not-found")
	}
}

func TestExchangeService_SeedExchanges_BadCSVPath(t *testing.T) {
	svc := NewExchangeService(newFakeExchangeSeedRepo(), newFakeSettingRepo())
	if err := svc.SeedExchanges("/nonexistent.csv"); err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestExchangeService_SeedExchanges_FromCSV(t *testing.T) {
	repo := newFakeExchangeSeedRepo()
	svc := NewExchangeService(repo, newFakeSettingRepo())
	if err := svc.SeedExchanges("../../data/exchanges.csv"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(repo.upsertedBy) == 0 {
		t.Error("expected at least one upsert")
	}
}

func TestExchangeService_SeedExchanges_UpsertErrorContinues(t *testing.T) {
	// upsert errors are logged but not returned
	repo := newFakeExchangeSeedRepo()
	repo.upsertErr = errors.New("dup key")
	svc := NewExchangeService(repo, newFakeSettingRepo())
	// As long as the CSV loads, SeedExchanges returns nil
	if err := svc.SeedExchanges("../../data/exchanges.csv"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// errExchangeSeedRepo is a minimal repo that always errors.
type errExchangeSeedRepo struct{ err error }

func (e *errExchangeSeedRepo) GetByID(_ uint64) (*model.StockExchange, error)      { return nil, e.err }
func (e *errExchangeSeedRepo) GetByAcronym(_ string) (*model.StockExchange, error) { return nil, e.err }
func (e *errExchangeSeedRepo) List(_ string, _, _ int) ([]model.StockExchange, int64, error) {
	return nil, 0, e.err
}
func (e *errExchangeSeedRepo) UpsertByMICCode(_ *model.StockExchange) error { return e.err }
