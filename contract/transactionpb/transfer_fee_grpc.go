// Hand-written gRPC service descriptor for FeeService.
// Since protoc is not available, this follows the same pattern as limit_grpc.go.
package transactionpb

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	FeeService_ListFees_FullMethodName   = "/transaction.FeeService/ListFees"
	FeeService_CreateFee_FullMethodName  = "/transaction.FeeService/CreateFee"
	FeeService_UpdateFee_FullMethodName  = "/transaction.FeeService/UpdateFee"
	FeeService_DeleteFee_FullMethodName  = "/transaction.FeeService/DeleteFee"
)

// FeeServiceClient is the client API for FeeService.
type FeeServiceClient interface {
	ListFees(ctx context.Context, in *ListFeesRequest, opts ...grpc.CallOption) (*ListFeesResponse, error)
	CreateFee(ctx context.Context, in *CreateFeeRequest, opts ...grpc.CallOption) (*TransferFeeResponse, error)
	UpdateFee(ctx context.Context, in *UpdateFeeRequest, opts ...grpc.CallOption) (*TransferFeeResponse, error)
	DeleteFee(ctx context.Context, in *DeleteFeeRequest, opts ...grpc.CallOption) (*DeleteFeeResponse, error)
}

type feeServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewFeeServiceClient(cc grpc.ClientConnInterface) FeeServiceClient {
	return &feeServiceClient{cc}
}

func (c *feeServiceClient) ListFees(ctx context.Context, in *ListFeesRequest, opts ...grpc.CallOption) (*ListFeesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListFeesResponse)
	err := c.cc.Invoke(ctx, FeeService_ListFees_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *feeServiceClient) CreateFee(ctx context.Context, in *CreateFeeRequest, opts ...grpc.CallOption) (*TransferFeeResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(TransferFeeResponse)
	err := c.cc.Invoke(ctx, FeeService_CreateFee_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *feeServiceClient) UpdateFee(ctx context.Context, in *UpdateFeeRequest, opts ...grpc.CallOption) (*TransferFeeResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(TransferFeeResponse)
	err := c.cc.Invoke(ctx, FeeService_UpdateFee_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *feeServiceClient) DeleteFee(ctx context.Context, in *DeleteFeeRequest, opts ...grpc.CallOption) (*DeleteFeeResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteFeeResponse)
	err := c.cc.Invoke(ctx, FeeService_DeleteFee_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FeeServiceServer is the server API for FeeService.
type FeeServiceServer interface {
	ListFees(context.Context, *ListFeesRequest) (*ListFeesResponse, error)
	CreateFee(context.Context, *CreateFeeRequest) (*TransferFeeResponse, error)
	UpdateFee(context.Context, *UpdateFeeRequest) (*TransferFeeResponse, error)
	DeleteFee(context.Context, *DeleteFeeRequest) (*DeleteFeeResponse, error)
	mustEmbedUnimplementedFeeServiceServer()
}

// UnimplementedFeeServiceServer must be embedded for forward compatibility.
type UnimplementedFeeServiceServer struct{}

func (UnimplementedFeeServiceServer) ListFees(context.Context, *ListFeesRequest) (*ListFeesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListFees not implemented")
}
func (UnimplementedFeeServiceServer) CreateFee(context.Context, *CreateFeeRequest) (*TransferFeeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateFee not implemented")
}
func (UnimplementedFeeServiceServer) UpdateFee(context.Context, *UpdateFeeRequest) (*TransferFeeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateFee not implemented")
}
func (UnimplementedFeeServiceServer) DeleteFee(context.Context, *DeleteFeeRequest) (*DeleteFeeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteFee not implemented")
}
func (UnimplementedFeeServiceServer) mustEmbedUnimplementedFeeServiceServer() {}
func (UnimplementedFeeServiceServer) testEmbeddedByValue()                    {}

// UnsafeFeeServiceServer may be embedded to opt out of forward compatibility.
type UnsafeFeeServiceServer interface {
	mustEmbedUnimplementedFeeServiceServer()
}

func RegisterFeeServiceServer(s grpc.ServiceRegistrar, srv FeeServiceServer) {
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&FeeService_ServiceDesc, srv)
}

func _FeeService_ListFees_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListFeesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FeeServiceServer).ListFees(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: FeeService_ListFees_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(FeeServiceServer).ListFees(ctx, req.(*ListFeesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _FeeService_CreateFee_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateFeeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FeeServiceServer).CreateFee(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: FeeService_CreateFee_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(FeeServiceServer).CreateFee(ctx, req.(*CreateFeeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _FeeService_UpdateFee_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateFeeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FeeServiceServer).UpdateFee(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: FeeService_UpdateFee_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(FeeServiceServer).UpdateFee(ctx, req.(*UpdateFeeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _FeeService_DeleteFee_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteFeeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FeeServiceServer).DeleteFee(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: FeeService_DeleteFee_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(FeeServiceServer).DeleteFee(ctx, req.(*DeleteFeeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// FeeService_ServiceDesc is the grpc.ServiceDesc for FeeService.
var FeeService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "transaction.FeeService",
	HandlerType: (*FeeServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "ListFees",
			Handler:    _FeeService_ListFees_Handler,
		},
		{
			MethodName: "CreateFee",
			Handler:    _FeeService_CreateFee_Handler,
		},
		{
			MethodName: "UpdateFee",
			Handler:    _FeeService_UpdateFee_Handler,
		},
		{
			MethodName: "DeleteFee",
			Handler:    _FeeService_DeleteFee_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "transaction/fee.proto",
}
