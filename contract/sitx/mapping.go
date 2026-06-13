package sitx

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// InternalPosting is the flat, decimal-string posting carried over gRPC to the
// executor. Direction is the INTERNAL effect (DEBIT = asset leaves / outgoing,
// CREDIT = asset arrives / incoming) — the inverse of the spec's bookkeeping
// word. Amount is the non-negative magnitude.
type InternalPosting struct {
	RoutingNumber int64
	AccountType   string // PERSON | ACCOUNT | OPTION
	AccountID     string
	AssetType     string // MONAS | STOCK | OPTION
	AssetID       string
	Direction     string // DirectionDebit | DirectionCredit
	Amount        string // decimal string, magnitude (>= 0)
}

// SpecPostingToInternal maps a spec Posting to the internal representation,
// applying the sign->direction inversion: spec negative (credit, asset leaves)
// -> internal DEBIT; spec positive (debit, asset arrives) -> internal CREDIT.
func SpecPostingToInternal(p Posting) (InternalPosting, error) {
	ip := InternalPosting{AccountType: p.Account.Type, AssetType: p.Asset.Type}

	switch p.Account.Type {
	case "ACCOUNT":
		ip.AccountID = p.Account.Num
		ip.RoutingNumber = routingFromAccountNumber(p.Account.Num)
	case "PERSON", "OPTION":
		if p.Account.ID == nil {
			return ip, fmt.Errorf("account type %s requires id", p.Account.Type)
		}
		ip.AccountID = p.Account.ID.ID
		ip.RoutingNumber = p.Account.ID.RoutingNumber
	default:
		return ip, fmt.Errorf("unknown account type %q", p.Account.Type)
	}

	assetID, err := assetToID(p.Asset)
	if err != nil {
		return ip, err
	}
	ip.AssetID = assetID

	amt := p.Amount.Decimal
	if amt.IsNegative() {
		ip.Direction = DirectionDebit // spec negative (credit/asset leaves) → internal DEBIT
	} else {
		ip.Direction = DirectionCredit // spec positive (debit/asset arrives) → internal CREDIT
	}
	ip.Amount = amt.Abs().String()
	return ip, nil
}

// InternalPostingToSpec is the inverse, used on the outbound path.
func InternalPostingToSpec(ip InternalPosting) (Posting, error) {
	var acc TxAccount
	switch ip.AccountType {
	case "ACCOUNT":
		acc = TxAccount{Type: "ACCOUNT", Num: ip.AccountID}
	case "PERSON", "OPTION":
		acc = TxAccount{Type: ip.AccountType, ID: &ForeignBankId{RoutingNumber: ip.RoutingNumber, ID: ip.AccountID}}
	default:
		return Posting{}, fmt.Errorf("unknown account type %q", ip.AccountType)
	}

	asset, err := idToAsset(ip.AssetType, ip.AssetID)
	if err != nil {
		return Posting{}, err
	}

	mag, err := decimal.NewFromString(ip.Amount)
	if err != nil {
		return Posting{}, err
	}
	signed := mag.Abs()
	if ip.Direction == DirectionDebit {
		signed = signed.Neg() // internal DEBIT → spec negative (credit/asset leaves)
	}
	return Posting{Account: acc, Amount: DecimalNumber{signed}, Asset: asset}, nil
}

func assetToID(a Asset) (string, error) {
	switch a.Type {
	case "MONAS":
		return fieldString(a.Asset, "currency")
	case "STOCK":
		return fieldString(a.Asset, "ticker")
	case "OPTION":
		b, err := json.Marshal(a.Asset)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("unknown asset type %q", a.Type)
	}
}

func idToAsset(assetType, assetID string) (Asset, error) {
	switch assetType {
	case "MONAS":
		return Asset{Type: "MONAS", Asset: MonetaryAsset{Currency: assetID}}, nil
	case "STOCK":
		return Asset{Type: "STOCK", Asset: StockDescription{Ticker: assetID}}, nil
	case "OPTION":
		var od OptionDescription
		if err := json.Unmarshal([]byte(assetID), &od); err != nil {
			return Asset{}, err
		}
		return Asset{Type: "OPTION", Asset: od}, nil
	default:
		return Asset{}, fmt.Errorf("unknown asset type %q", assetType)
	}
}

// fieldString reads a string field from either a typed struct (marshalled) or a
// map[string]interface{} (as produced by json.Unmarshal into Asset.Asset).
func fieldString(v interface{}, key string) (string, error) {
	switch m := v.(type) {
	case map[string]interface{}:
		s, _ := m[key].(string)
		return s, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		var mm map[string]interface{}
		if err := json.Unmarshal(b, &mm); err != nil {
			return "", err
		}
		s, _ := mm[key].(string)
		return s, nil
	}
}

// routingFromAccountNumber reads the 3-digit routing prefix of an account
// number; returns 0 if too short or non-numeric.
func routingFromAccountNumber(num string) int64 {
	if len(num) < 3 {
		return 0
	}
	var r int64
	for _, c := range num[:3] {
		if c < '0' || c > '9' {
			return 0
		}
		r = r*10 + int64(c-'0')
	}
	return r
}
