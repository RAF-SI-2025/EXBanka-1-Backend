// Hand-written gRPC service descriptor for BankAccountService.
// Since protoc is not available, this follows the same pattern as account_grpc.pb.go.
package accountpb

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	BankAccountService_CreateBankAccount_FullMethodName  = "/account.BankAccountService/CreateBankAccount"
	BankAccountService_ListBankAccounts_FullMethodName   = "/account.BankAccountService/ListBankAccounts"
	BankAccountService_DeleteBankAccount_FullMethodName  = "/account.BankAccountService/DeleteBankAccount"
	BankAccountService_GetBankRSDAccount_FullMethodName  = "/account.BankAccountService/GetBankRSDAccount"
)

// BankAccountServiceClient is the client API for BankAccountService.
type BankAccountServiceClient interface {
	CreateBankAccount(ctx context.Context, in *CreateBankAccountRequest, opts ...grpc.CallOption) (*AccountResponse, error)
	ListBankAccounts(ctx context.Context, in *ListBankAccountsRequest, opts ...grpc.CallOption) (*ListBankAccountsResponse, error)
	DeleteBankAccount(ctx context.Context, in *DeleteBankAccountRequest, opts ...grpc.CallOption) (*DeleteBankAccountResponse, error)
	GetBankRSDAccount(ctx context.Context, in *GetBankRSDAccountRequest, opts ...grpc.CallOption) (*AccountResponse, error)
}

type bankAccountServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewBankAccountServiceClient(cc grpc.ClientConnInterface) BankAccountServiceClient {
	return &bankAccountServiceClient{cc}
}

func (c *bankAccountServiceClient) CreateBankAccount(ctx context.Context, in *CreateBankAccountRequest, opts ...grpc.CallOption) (*AccountResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(AccountResponse)
	err := c.cc.Invoke(ctx, BankAccountService_CreateBankAccount_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankAccountServiceClient) ListBankAccounts(ctx context.Context, in *ListBankAccountsRequest, opts ...grpc.CallOption) (*ListBankAccountsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListBankAccountsResponse)
	err := c.cc.Invoke(ctx, BankAccountService_ListBankAccounts_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankAccountServiceClient) DeleteBankAccount(ctx context.Context, in *DeleteBankAccountRequest, opts ...grpc.CallOption) (*DeleteBankAccountResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteBankAccountResponse)
	err := c.cc.Invoke(ctx, BankAccountService_DeleteBankAccount_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankAccountServiceClient) GetBankRSDAccount(ctx context.Context, in *GetBankRSDAccountRequest, opts ...grpc.CallOption) (*AccountResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(AccountResponse)
	err := c.cc.Invoke(ctx, BankAccountService_GetBankRSDAccount_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BankAccountServiceServer is the server API for BankAccountService.
type BankAccountServiceServer interface {
	CreateBankAccount(context.Context, *CreateBankAccountRequest) (*AccountResponse, error)
	ListBankAccounts(context.Context, *ListBankAccountsRequest) (*ListBankAccountsResponse, error)
	DeleteBankAccount(context.Context, *DeleteBankAccountRequest) (*DeleteBankAccountResponse, error)
	GetBankRSDAccount(context.Context, *GetBankRSDAccountRequest) (*AccountResponse, error)
	mustEmbedUnimplementedBankAccountServiceServer()
}

// UnimplementedBankAccountServiceServer must be embedded for forward compatibility.
type UnimplementedBankAccountServiceServer struct{}

func (UnimplementedBankAccountServiceServer) CreateBankAccount(context.Context, *CreateBankAccountRequest) (*AccountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateBankAccount not implemented")
}
func (UnimplementedBankAccountServiceServer) ListBankAccounts(context.Context, *ListBankAccountsRequest) (*ListBankAccountsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListBankAccounts not implemented")
}
func (UnimplementedBankAccountServiceServer) DeleteBankAccount(context.Context, *DeleteBankAccountRequest) (*DeleteBankAccountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteBankAccount not implemented")
}
func (UnimplementedBankAccountServiceServer) GetBankRSDAccount(context.Context, *GetBankRSDAccountRequest) (*AccountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBankRSDAccount not implemented")
}
func (UnimplementedBankAccountServiceServer) mustEmbedUnimplementedBankAccountServiceServer() {}
func (UnimplementedBankAccountServiceServer) testEmbeddedByValue()                            {}

// UnsafeBankAccountServiceServer may be embedded to opt out of forward compatibility.
type UnsafeBankAccountServiceServer interface {
	mustEmbedUnimplementedBankAccountServiceServer()
}

func RegisterBankAccountServiceServer(s grpc.ServiceRegistrar, srv BankAccountServiceServer) {
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&BankAccountService_ServiceDesc, srv)
}

func _BankAccountService_CreateBankAccount_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateBankAccountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankAccountServiceServer).CreateBankAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankAccountService_CreateBankAccount_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankAccountServiceServer).CreateBankAccount(ctx, req.(*CreateBankAccountRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankAccountService_ListBankAccounts_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListBankAccountsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankAccountServiceServer).ListBankAccounts(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankAccountService_ListBankAccounts_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankAccountServiceServer).ListBankAccounts(ctx, req.(*ListBankAccountsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankAccountService_DeleteBankAccount_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteBankAccountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankAccountServiceServer).DeleteBankAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankAccountService_DeleteBankAccount_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankAccountServiceServer).DeleteBankAccount(ctx, req.(*DeleteBankAccountRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankAccountService_GetBankRSDAccount_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBankRSDAccountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankAccountServiceServer).GetBankRSDAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankAccountService_GetBankRSDAccount_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankAccountServiceServer).GetBankRSDAccount(ctx, req.(*GetBankRSDAccountRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// BankAccountService_ServiceDesc is the grpc.ServiceDesc for BankAccountService.
var BankAccountService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "account.BankAccountService",
	HandlerType: (*BankAccountServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateBankAccount",
			Handler:    _BankAccountService_CreateBankAccount_Handler,
		},
		{
			MethodName: "ListBankAccounts",
			Handler:    _BankAccountService_ListBankAccounts_Handler,
		},
		{
			MethodName: "DeleteBankAccount",
			Handler:    _BankAccountService_DeleteBankAccount_Handler,
		},
		{
			MethodName: "GetBankRSDAccount",
			Handler:    _BankAccountService_GetBankRSDAccount_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "account/bank_account.proto",
}
