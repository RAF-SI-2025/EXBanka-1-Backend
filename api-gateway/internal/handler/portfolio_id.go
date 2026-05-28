package handler

import (
	"fmt"
	"regexp"
	"strconv"
)

var (
	rxClient = regexp.MustCompile(`^client-(\d+)$`)
	rxFund   = regexp.MustCompile(`^fund-(\d+)$`)
)

// EncodePortfolioID returns the URL-safe string form of a portfolio identity.
//
//	client owner    -> "client-<id>"
//	bank owner      -> "bank"          (no id; singleton)
//	investment_fund -> "fund-<id>"
func EncodePortfolioID(ownerType string, ownerID *uint64) (string, error) {
	switch ownerType {
	case "client":
		if ownerID == nil {
			return "", fmt.Errorf("client portfolio requires owner_id")
		}
		return fmt.Sprintf("client-%d", *ownerID), nil
	case "bank":
		return "bank", nil
	case "investment_fund":
		if ownerID == nil {
			return "", fmt.Errorf("fund portfolio requires owner_id")
		}
		return fmt.Sprintf("fund-%d", *ownerID), nil
	default:
		return "", fmt.Errorf("unknown owner type %q", ownerType)
	}
}

// DecodePortfolioID parses the URL form back into (ownerType, ownerID).
// Owner IDs must be > 0. Returns a 400-compatible error string on bad input.
func DecodePortfolioID(s string) (string, *uint64, error) {
	if s == "bank" {
		return "bank", nil, nil
	}
	if m := rxClient.FindStringSubmatch(s); m != nil {
		id, _ := strconv.ParseUint(m[1], 10, 64)
		if id == 0 {
			return "", nil, fmt.Errorf("invalid portfolio id: client id must be > 0")
		}
		return "client", &id, nil
	}
	if m := rxFund.FindStringSubmatch(s); m != nil {
		id, _ := strconv.ParseUint(m[1], 10, 64)
		if id == 0 {
			return "", nil, fmt.Errorf("invalid portfolio id: fund id must be > 0")
		}
		return "investment_fund", &id, nil
	}
	return "", nil, fmt.Errorf("invalid portfolio id %q", s)
}
