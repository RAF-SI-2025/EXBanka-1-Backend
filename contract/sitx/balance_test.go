package sitx

import (
	"testing"

	"github.com/shopspring/decimal"
)

func monas(num, amt, ccy string) Posting {
	return Posting{
		Account: TxAccount{Type: "ACCOUNT", Num: num},
		Amount:  DecimalNumber{decimal.RequireFromString(amt)},
		Asset:   Asset{Type: "MONAS", Asset: MonetaryAsset{Currency: ccy}},
	}
}

func TestIsBalanced_TrueWhenSumZeroPerAsset(t *testing.T) {
	tx := []Posting{monas("444", "-260", "RSD"), monas("111", "260", "RSD")}
	if !IsBalanced(tx) {
		t.Fatal("want balanced")
	}
}

func TestIsBalanced_FalseWhenAssetSumNonZero(t *testing.T) {
	tx := []Posting{monas("444", "-260", "RSD"), monas("111", "100", "RSD")}
	if IsBalanced(tx) {
		t.Fatal("want unbalanced")
	}
}

func TestIsBalanced_PerAssetIndependent(t *testing.T) {
	// EUR balances, RSD does not.
	tx := []Posting{monas("1", "-5", "EUR"), monas("2", "5", "EUR"), monas("3", "-9", "RSD")}
	if IsBalanced(tx) {
		t.Fatal("want unbalanced (RSD off)")
	}
}

func TestIsBalanced_MultiAssetBalanced(t *testing.T) {
	// RSD balances AND a STOCK leg balances.
	stock := func(amt string) Posting {
		return Posting{
			Account: TxAccount{Type: "OPTION", ID: &ForeignBankId{RoutingNumber: 222, ID: "neg-1"}},
			Amount:  DecimalNumber{decimal.RequireFromString(amt)},
			Asset:   Asset{Type: "STOCK", Asset: StockDescription{Ticker: "AAPL"}},
		}
	}
	tx := []Posting{monas("1", "-5", "EUR"), monas("2", "5", "EUR"), stock("-3"), stock("3")}
	if !IsBalanced(tx) {
		t.Fatal("want balanced across EUR + AAPL")
	}
}
