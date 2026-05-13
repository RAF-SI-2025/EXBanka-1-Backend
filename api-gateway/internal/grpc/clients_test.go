package grpc

import "testing"

// addr that resolves a valid format but will never connect; grpc.Dial is
// non-blocking so the constructors should still succeed.
const dummyAddr = "127.0.0.1:1"

func TestSagaDialReturnsConn(t *testing.T) {
	conn, err := sagaDial(dummyAddr)
	if err != nil {
		t.Fatalf("sagaDial: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}
	_ = conn.Close()
}

func TestNewClients(t *testing.T) {
	cases := []struct {
		name string
		fn   func(addr string) error
	}{
		{"AuthClient", func(a string) error {
			c, conn, err := NewAuthClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"UserClient", func(a string) error {
			c, conn, err := NewUserClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"ActuaryClient", func(a string) error {
			c, conn, err := NewActuaryClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"ClientClient", func(a string) error {
			c, conn, err := NewClientClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"AccountClient", func(a string) error {
			c, conn, err := NewAccountClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"BankAccountClient", func(a string) error {
			c, conn, err := NewBankAccountClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"CardClient", func(a string) error {
			c, conn, err := NewCardClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"VirtualCardClient", func(a string) error {
			c, conn, err := NewVirtualCardClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"CardRequestClient", func(a string) error {
			c, conn, err := NewCardRequestClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"TransactionClient", func(a string) error {
			c, conn, err := NewTransactionClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"FeeServiceClient", func(a string) error {
			c, conn, err := NewFeeServiceClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"CreditClient", func(a string) error {
			c, conn, err := NewCreditClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"ExchangeClient", func(a string) error {
			c, conn, err := NewExchangeClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"NotificationClient", func(a string) error {
			c, conn, err := NewNotificationClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"VerificationClient", func(a string) error {
			c, conn, err := NewVerificationClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"BlueprintClient", func(a string) error {
			c, conn, err := NewBlueprintClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"EmployeeLimitClient", func(a string) error {
			c, conn, err := NewEmployeeLimitClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"ClientLimitClient", func(a string) error {
			c, conn, err := NewClientLimitClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"PeerTxServiceClient", func(a string) error {
			c, conn, err := NewPeerTxServiceClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"PeerBankAdminServiceClient", func(a string) error {
			c, conn, err := NewPeerBankAdminServiceClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"PeerOTCServiceClient", func(a string) error {
			c, conn, err := NewPeerOTCServiceClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"StockExchangeClient", func(a string) error {
			c, conn, err := NewStockExchangeClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"SecurityClient", func(a string) error {
			c, conn, err := NewSecurityClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"OrderClient", func(a string) error {
			c, conn, err := NewOrderClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"PortfolioClient", func(a string) error {
			c, conn, err := NewPortfolioClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"OTCClient", func(a string) error {
			c, conn, err := NewOTCClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"TaxClient", func(a string) error {
			c, conn, err := NewTaxClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"SourceAdminClient", func(a string) error {
			c, conn, err := NewSourceAdminClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"InvestmentFundClient", func(a string) error {
			c, conn, err := NewInvestmentFundClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
		{"OTCOptionsClient", func(a string) error {
			c, conn, err := NewOTCOptionsClient(a)
			if err == nil && (c == nil || conn == nil) {
				t.Fatal("nil client/conn")
			}
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(dummyAddr); err != nil {
				t.Fatalf("%s err: %v", c.name, err)
			}
		})
	}
}
