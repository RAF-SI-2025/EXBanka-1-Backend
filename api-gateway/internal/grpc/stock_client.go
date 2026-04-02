package grpc

import (
	stockpb "github.com/exbanka/contract/stockpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func NewStockExchangeClient(addr string) (stockpb.StockExchangeGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewStockExchangeGRPCServiceClient(conn), conn, nil
}

func NewSecurityClient(addr string) (stockpb.SecurityGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewSecurityGRPCServiceClient(conn), conn, nil
}

func NewOrderClient(addr string) (stockpb.OrderGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewOrderGRPCServiceClient(conn), conn, nil
}

func NewPortfolioClient(addr string) (stockpb.PortfolioGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewPortfolioGRPCServiceClient(conn), conn, nil
}

func NewOTCClient(addr string) (stockpb.OTCGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewOTCGRPCServiceClient(conn), conn, nil
}

func NewTaxClient(addr string) (stockpb.TaxGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return stockpb.NewTaxGRPCServiceClient(conn), conn, nil
}
