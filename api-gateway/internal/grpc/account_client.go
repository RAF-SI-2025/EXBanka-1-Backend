package grpc

import (
	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/shared"
)

func NewAccountClient(addr string) (accountpb.AccountServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return accountpb.NewAccountServiceClient(conn), conn, nil
}

func NewBankAccountClient(addr string) (accountpb.BankAccountServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return accountpb.NewBankAccountServiceClient(conn), conn, nil
}
