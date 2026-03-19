// Hand-written gRPC service descriptor for ClientLimitService.
// Since protoc is not available, this follows the same pattern as client_grpc.pb.go.
package clientpb

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	ClientLimitService_GetClientLimits_FullMethodName = "/client.ClientLimitService/GetClientLimits"
	ClientLimitService_SetClientLimits_FullMethodName = "/client.ClientLimitService/SetClientLimits"
)

// ClientLimitServiceClient is the client API for ClientLimitService.
type ClientLimitServiceClient interface {
	GetClientLimits(ctx context.Context, in *GetClientLimitRequest, opts ...grpc.CallOption) (*ClientLimitResponse, error)
	SetClientLimits(ctx context.Context, in *SetClientLimitRequest, opts ...grpc.CallOption) (*ClientLimitResponse, error)
}

type clientLimitServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewClientLimitServiceClient(cc grpc.ClientConnInterface) ClientLimitServiceClient {
	return &clientLimitServiceClient{cc}
}

func (c *clientLimitServiceClient) GetClientLimits(ctx context.Context, in *GetClientLimitRequest, opts ...grpc.CallOption) (*ClientLimitResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ClientLimitResponse)
	err := c.cc.Invoke(ctx, ClientLimitService_GetClientLimits_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clientLimitServiceClient) SetClientLimits(ctx context.Context, in *SetClientLimitRequest, opts ...grpc.CallOption) (*ClientLimitResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ClientLimitResponse)
	err := c.cc.Invoke(ctx, ClientLimitService_SetClientLimits_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClientLimitServiceServer is the server API for ClientLimitService.
type ClientLimitServiceServer interface {
	GetClientLimits(context.Context, *GetClientLimitRequest) (*ClientLimitResponse, error)
	SetClientLimits(context.Context, *SetClientLimitRequest) (*ClientLimitResponse, error)
	mustEmbedUnimplementedClientLimitServiceServer()
}

// UnimplementedClientLimitServiceServer must be embedded for forward compatibility.
type UnimplementedClientLimitServiceServer struct{}

func (UnimplementedClientLimitServiceServer) GetClientLimits(context.Context, *GetClientLimitRequest) (*ClientLimitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetClientLimits not implemented")
}
func (UnimplementedClientLimitServiceServer) SetClientLimits(context.Context, *SetClientLimitRequest) (*ClientLimitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method SetClientLimits not implemented")
}
func (UnimplementedClientLimitServiceServer) mustEmbedUnimplementedClientLimitServiceServer() {}
func (UnimplementedClientLimitServiceServer) testEmbeddedByValue()                            {}

// UnsafeClientLimitServiceServer may be embedded to opt out of forward compatibility.
type UnsafeClientLimitServiceServer interface {
	mustEmbedUnimplementedClientLimitServiceServer()
}

func RegisterClientLimitServiceServer(s grpc.ServiceRegistrar, srv ClientLimitServiceServer) {
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&ClientLimitService_ServiceDesc, srv)
}

func _ClientLimitService_GetClientLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetClientLimitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClientLimitServiceServer).GetClientLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ClientLimitService_GetClientLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClientLimitServiceServer).GetClientLimits(ctx, req.(*GetClientLimitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClientLimitService_SetClientLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SetClientLimitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClientLimitServiceServer).SetClientLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ClientLimitService_SetClientLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClientLimitServiceServer).SetClientLimits(ctx, req.(*SetClientLimitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// ClientLimitService_ServiceDesc is the grpc.ServiceDesc for ClientLimitService.
var ClientLimitService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "client.ClientLimitService",
	HandlerType: (*ClientLimitServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetClientLimits",
			Handler:    _ClientLimitService_GetClientLimits_Handler,
		},
		{
			MethodName: "SetClientLimits",
			Handler:    _ClientLimitService_SetClientLimits_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "client/client_limit.proto",
}
