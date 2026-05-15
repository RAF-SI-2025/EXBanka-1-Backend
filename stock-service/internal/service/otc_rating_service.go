package service

import (
	"errors"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCRatingService wires the OTC trader-rating repo with the OTC offer
// repo (used to verify the offer is ACCEPTED and the caller is a party
// before recording a rating).
type OTCRatingService struct {
	ratings *repository.OTCTraderRatingRepository
	offers  *repository.OTCOfferRepository
}

func NewOTCRatingService(ratings *repository.OTCTraderRatingRepository, offers *repository.OTCOfferRepository) *OTCRatingService {
	return &OTCRatingService{ratings: ratings, offers: offers}
}

var (
	ErrRatingOfferNotFound = svcerr.New(codes.NotFound, "otc offer not found")
	ErrRatingNotAccepted   = svcerr.New(codes.FailedPrecondition, "can only rate ACCEPTED offers")
	ErrRatingNotParticipant = svcerr.New(codes.PermissionDenied, "rater must be a party to the offer")
	ErrRatingAlreadyExists = svcerr.New(codes.AlreadyExists, "rating already submitted for this offer")
)

// SubmitInput captures the caller-supplied fields for a new rating.
// The rated party is derived from the offer (the side opposite the rater).
type SubmitInput struct {
	OfferID        uint64
	RaterOwnerType model.OwnerType
	RaterOwnerID   *uint64
	Score          int
	Comment        string
}

// Submit creates a new rating after verifying the offer is ACCEPTED and
// the rater is one of the two parties.
func (s *OTCRatingService) Submit(in SubmitInput) (*model.OTCTraderRating, error) {
	offer, err := s.offers.GetByID(in.OfferID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRatingOfferNotFound
		}
		return nil, err
	}
	if offer.Status != model.OTCOfferStatusAccepted {
		return nil, ErrRatingNotAccepted
	}

	// Determine which side the rater is on; rated is the opposite side.
	var ratedType model.OwnerType
	var ratedID *uint64
	switch {
	case offer.InitiatorOwnerType == in.RaterOwnerType && ownerIDEqual(offer.InitiatorOwnerID, in.RaterOwnerID):
		if offer.CounterpartyOwnerType == nil {
			return nil, ErrRatingNotParticipant
		}
		ratedType = *offer.CounterpartyOwnerType
		ratedID = offer.CounterpartyOwnerID
	case offer.CounterpartyOwnerType != nil && *offer.CounterpartyOwnerType == in.RaterOwnerType && ownerIDEqual(offer.CounterpartyOwnerID, in.RaterOwnerID):
		ratedType = offer.InitiatorOwnerType
		ratedID = offer.InitiatorOwnerID
	default:
		return nil, ErrRatingNotParticipant
	}

	row := &model.OTCTraderRating{
		OfferID:        offer.ID,
		RaterOwnerType: in.RaterOwnerType,
		RaterOwnerID:   in.RaterOwnerID,
		RatedOwnerType: ratedType,
		RatedOwnerID:   ratedID,
		Score:          in.Score,
		Comment:        in.Comment,
	}
	if err := s.ratings.Create(row); err != nil {
		if errors.Is(err, repository.ErrRatingAlreadyExists) {
			return nil, ErrRatingAlreadyExists
		}
		return nil, err
	}
	return row, nil
}

// TraderProfile is the response shape for GetProfile.
type TraderProfile struct {
	OwnerType model.OwnerType
	OwnerID   *uint64
	Avg       float64
	Count     int64
	Recent    []model.OTCTraderRating
}

// GetProfile returns the aggregate rating + the most recent N comments
// for the (owner_type, owner_id) trader.
func (s *OTCRatingService) GetProfile(ratedType model.OwnerType, ratedID *uint64, recentLimit int) (*TraderProfile, error) {
	avg, count, err := s.ratings.AvgForRated(ratedType, ratedID)
	if err != nil {
		return nil, err
	}
	recent, err := s.ratings.ListForRated(ratedType, ratedID, recentLimit)
	if err != nil {
		return nil, err
	}
	return &TraderProfile{
		OwnerType: ratedType,
		OwnerID:   ratedID,
		Avg:       avg,
		Count:     count,
		Recent:    recent,
	}, nil
}

// ListReceived returns the ratings a caller has received (no aggregates).
func (s *OTCRatingService) ListReceived(ratedType model.OwnerType, ratedID *uint64, limit int) ([]model.OTCTraderRating, error) {
	return s.ratings.ListForRated(ratedType, ratedID, limit)
}
