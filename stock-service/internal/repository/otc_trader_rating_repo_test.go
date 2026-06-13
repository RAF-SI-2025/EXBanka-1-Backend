// Tests for OTCTraderRatingRepository.
package repository

import (
	"testing"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newOTCTraderRatingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCTraderRating{}); err != nil {
		t.Fatalf("migrate otc_trader_ratings: %v", err)
	}
	return db
}

func TestOTCTraderRatingRepository_Create_And_AvgForRated(t *testing.T) {
	db := newOTCTraderRatingTestDB(t)
	r := NewOTCTraderRatingRepository(db)

	raterID := uint64(1)
	ratedID := uint64(2)

	// Create two ratings for the same ratedID
	for _, score := range []int{4, 2} {
		row := &model.OTCTraderRating{
			OfferID:        uint64(score), // unique per (offer,rater) pair
			RaterOwnerType: model.OwnerClient,
			RaterOwnerID:   &raterID,
			RatedOwnerType: model.OwnerClient,
			RatedOwnerID:   &ratedID,
			Score:          score,
			Comment:        "test",
		}
		if err := r.Create(row); err != nil {
			t.Fatalf("create score=%d: %v", score, err)
		}
		if row.ID == 0 {
			t.Errorf("expected non-zero id for score=%d", score)
		}
	}

	avg, count, err := r.AvgForRated(model.OwnerClient, &ratedID)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
	if avg != 3.0 {
		t.Errorf("expected avg=3.0, got %f", avg)
	}
}

func TestOTCTraderRatingRepository_AvgForRated_Empty(t *testing.T) {
	db := newOTCTraderRatingTestDB(t)
	r := NewOTCTraderRatingRepository(db)

	noID := uint64(9999)
	avg, count, err := r.AvgForRated(model.OwnerClient, &noID)
	if err != nil {
		t.Fatalf("avg empty: %v", err)
	}
	if avg != 0 || count != 0 {
		t.Errorf("expected (0,0), got (%f,%d)", avg, count)
	}
}

func TestOTCTraderRatingRepository_Create_Duplicate(t *testing.T) {
	db := newOTCTraderRatingTestDB(t)
	r := NewOTCTraderRatingRepository(db)

	raterID := uint64(1)
	ratedID := uint64(2)

	row := &model.OTCTraderRating{
		OfferID:        10,
		RaterOwnerType: model.OwnerClient,
		RaterOwnerID:   &raterID,
		RatedOwnerType: model.OwnerClient,
		RatedOwnerID:   &ratedID,
		Score:          5,
	}
	if err := r.Create(row); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Duplicate (same offer+rater) should return ErrRatingAlreadyExists
	dup := &model.OTCTraderRating{
		OfferID:        10,
		RaterOwnerType: model.OwnerClient,
		RaterOwnerID:   &raterID,
		RatedOwnerType: model.OwnerClient,
		RatedOwnerID:   &ratedID,
		Score:          3,
	}
	err := r.Create(dup)
	if err != ErrRatingAlreadyExists {
		t.Errorf("expected ErrRatingAlreadyExists, got %v", err)
	}
}

func TestOTCTraderRatingRepository_ListForRated(t *testing.T) {
	db := newOTCTraderRatingTestDB(t)
	r := NewOTCTraderRatingRepository(db)

	ratedID := uint64(3)
	for offerID, score := range []int{5, 4, 3, 2, 1} {
		raterID := uint64(offerID + 10)
		row := &model.OTCTraderRating{
			OfferID:        uint64(offerID + 1),
			RaterOwnerType: model.OwnerClient,
			RaterOwnerID:   &raterID,
			RatedOwnerType: model.OwnerClient,
			RatedOwnerID:   &ratedID,
			Score:          score,
		}
		if err := r.Create(row); err != nil {
			t.Fatalf("create offerID=%d: %v", offerID, err)
		}
	}

	rows, err := r.ListForRated(model.OwnerClient, &ratedID, 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3, got %d", len(rows))
	}

	// limit 0 → defaults to 20
	all, err := r.ListForRated(model.OwnerClient, &ratedID, 0)
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5 with default limit, got %d", len(all))
	}

	// limit > 100 → capped to 20
	all2, err := r.ListForRated(model.OwnerClient, &ratedID, 200)
	if err != nil {
		t.Fatalf("list capped: %v", err)
	}
	if len(all2) != 5 {
		t.Errorf("expected 5 with capped limit, got %d", len(all2))
	}
}
