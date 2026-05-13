package router

import (
	"testing"
)

func TestNewHandlers_AssemblesEveryHandler(t *testing.T) {
	deps := Deps{
		OwnBankCode: "111",
	}
	h := NewHandlers(deps)
	if h == nil {
		t.Fatal("nil handlers")
	}
	if h.Auth == nil || h.Employee == nil || h.Role == nil || h.Limit == nil {
		t.Fatal("auth/employee/role/limit nil")
	}
	if h.Client == nil || h.Account == nil || h.Card == nil || h.Tx == nil {
		t.Fatal("client/account/card/tx nil")
	}
	if h.Exchange == nil || h.Credit == nil || h.Me == nil || h.Session == nil {
		t.Fatal("exchange/credit/me/session nil")
	}
	if h.StockExchange == nil || h.Securities == nil || h.StockOrder == nil || h.Portfolio == nil {
		t.Fatal("stock handlers nil")
	}
	if h.Actuary == nil || h.Blueprint == nil || h.Tax == nil || h.StockSource == nil {
		t.Fatal("actuary/blueprint/tax/stocksource nil")
	}
	if h.Notification == nil || h.MobileAuth == nil || h.Verification == nil {
		t.Fatal("notif/mobile/verification nil")
	}
	if h.OptionsV2 == nil || h.Fund == nil || h.OTCOptions == nil {
		t.Fatal("optionsv2/fund/otcoptions nil")
	}
	if h.PeerTxDispatcher == nil || h.Changelog == nil {
		t.Fatal("peertx/changelog nil")
	}
	if h.PeerTx == nil || h.PeerBankAdmin == nil || h.PeerAuthMW == nil {
		t.Fatal("peer wiring nil")
	}
	if h.PeerOTC == nil || h.PeerUser == nil || h.PeerOTCInitiate == nil {
		t.Fatal("peer otc wiring nil")
	}
}

func TestNewHandlers_DefaultBankCode(t *testing.T) {
	h := NewHandlers(Deps{OwnBankCode: ""})
	if h == nil {
		t.Fatal("nil")
	}
}
