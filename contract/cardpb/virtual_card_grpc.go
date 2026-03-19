// Hand-written gRPC service descriptor for virtual card, PIN, and temporary block RPCs.
package cardpb

import (
	"context"

	"google.golang.org/grpc"
)

// VirtualCardServiceClient is the client API for VirtualCardService.
type VirtualCardServiceClient interface {
	CreateVirtualCard(ctx context.Context, in *CreateVirtualCardRequest, opts ...grpc.CallOption) (*CardResponse, error)
	SetCardPin(ctx context.Context, in *SetCardPinRequest, opts ...grpc.CallOption) (*SetCardPinResponse, error)
	VerifyCardPin(ctx context.Context, in *VerifyCardPinRequest, opts ...grpc.CallOption) (*VerifyCardPinResponse, error)
	TemporaryBlockCard(ctx context.Context, in *TemporaryBlockCardRequest, opts ...grpc.CallOption) (*CardResponse, error)
	UseCard(ctx context.Context, in *UseCardRequest, opts ...grpc.CallOption) (*UseCardResponse, error)
}

type virtualCardServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewVirtualCardServiceClient(cc grpc.ClientConnInterface) VirtualCardServiceClient {
	return &virtualCardServiceClient{cc}
}

func (c *virtualCardServiceClient) CreateVirtualCard(ctx context.Context, in *CreateVirtualCardRequest, opts ...grpc.CallOption) (*CardResponse, error) {
	out := new(CardResponse)
	err := c.cc.Invoke(ctx, "/card.VirtualCardService/CreateVirtualCard", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *virtualCardServiceClient) SetCardPin(ctx context.Context, in *SetCardPinRequest, opts ...grpc.CallOption) (*SetCardPinResponse, error) {
	out := new(SetCardPinResponse)
	err := c.cc.Invoke(ctx, "/card.VirtualCardService/SetCardPin", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *virtualCardServiceClient) VerifyCardPin(ctx context.Context, in *VerifyCardPinRequest, opts ...grpc.CallOption) (*VerifyCardPinResponse, error) {
	out := new(VerifyCardPinResponse)
	err := c.cc.Invoke(ctx, "/card.VirtualCardService/VerifyCardPin", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *virtualCardServiceClient) TemporaryBlockCard(ctx context.Context, in *TemporaryBlockCardRequest, opts ...grpc.CallOption) (*CardResponse, error) {
	out := new(CardResponse)
	err := c.cc.Invoke(ctx, "/card.VirtualCardService/TemporaryBlockCard", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *virtualCardServiceClient) UseCard(ctx context.Context, in *UseCardRequest, opts ...grpc.CallOption) (*UseCardResponse, error) {
	out := new(UseCardResponse)
	err := c.cc.Invoke(ctx, "/card.VirtualCardService/UseCard", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// VirtualCardServiceServer is the server API for VirtualCardService.
type VirtualCardServiceServer interface {
	CreateVirtualCard(context.Context, *CreateVirtualCardRequest) (*CardResponse, error)
	SetCardPin(context.Context, *SetCardPinRequest) (*SetCardPinResponse, error)
	VerifyCardPin(context.Context, *VerifyCardPinRequest) (*VerifyCardPinResponse, error)
	TemporaryBlockCard(context.Context, *TemporaryBlockCardRequest) (*CardResponse, error)
	UseCard(context.Context, *UseCardRequest) (*UseCardResponse, error)
	mustEmbedUnimplementedVirtualCardServiceServer()
}

// UnimplementedVirtualCardServiceServer must be embedded to have forward compatible implementations.
type UnimplementedVirtualCardServiceServer struct{}

func (UnimplementedVirtualCardServiceServer) CreateVirtualCard(context.Context, *CreateVirtualCardRequest) (*CardResponse, error) {
	return nil, nil
}
func (UnimplementedVirtualCardServiceServer) SetCardPin(context.Context, *SetCardPinRequest) (*SetCardPinResponse, error) {
	return nil, nil
}
func (UnimplementedVirtualCardServiceServer) VerifyCardPin(context.Context, *VerifyCardPinRequest) (*VerifyCardPinResponse, error) {
	return nil, nil
}
func (UnimplementedVirtualCardServiceServer) TemporaryBlockCard(context.Context, *TemporaryBlockCardRequest) (*CardResponse, error) {
	return nil, nil
}
func (UnimplementedVirtualCardServiceServer) UseCard(context.Context, *UseCardRequest) (*UseCardResponse, error) {
	return nil, nil
}
func (UnimplementedVirtualCardServiceServer) mustEmbedUnimplementedVirtualCardServiceServer() {}

// RegisterVirtualCardServiceServer registers the VirtualCardServiceServer with the gRPC server.
func RegisterVirtualCardServiceServer(s *grpc.Server, srv VirtualCardServiceServer) {
	s.RegisterService(&_VirtualCardService_serviceDesc, srv)
}

var _VirtualCardService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "card.VirtualCardService",
	HandlerType: (*VirtualCardServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateVirtualCard",
			Handler:    _VirtualCardService_CreateVirtualCard_Handler,
		},
		{
			MethodName: "SetCardPin",
			Handler:    _VirtualCardService_SetCardPin_Handler,
		},
		{
			MethodName: "VerifyCardPin",
			Handler:    _VirtualCardService_VerifyCardPin_Handler,
		},
		{
			MethodName: "TemporaryBlockCard",
			Handler:    _VirtualCardService_TemporaryBlockCard_Handler,
		},
		{
			MethodName: "UseCard",
			Handler:    _VirtualCardService_UseCard_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "card/virtual_card.proto",
}

func _VirtualCardService_CreateVirtualCard_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateVirtualCardRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(VirtualCardServiceServer).CreateVirtualCard(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/card.VirtualCardService/CreateVirtualCard"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(VirtualCardServiceServer).CreateVirtualCard(ctx, req.(*CreateVirtualCardRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _VirtualCardService_SetCardPin_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SetCardPinRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(VirtualCardServiceServer).SetCardPin(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/card.VirtualCardService/SetCardPin"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(VirtualCardServiceServer).SetCardPin(ctx, req.(*SetCardPinRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _VirtualCardService_VerifyCardPin_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(VerifyCardPinRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(VirtualCardServiceServer).VerifyCardPin(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/card.VirtualCardService/VerifyCardPin"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(VirtualCardServiceServer).VerifyCardPin(ctx, req.(*VerifyCardPinRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _VirtualCardService_TemporaryBlockCard_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TemporaryBlockCardRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(VirtualCardServiceServer).TemporaryBlockCard(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/card.VirtualCardService/TemporaryBlockCard"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(VirtualCardServiceServer).TemporaryBlockCard(ctx, req.(*TemporaryBlockCardRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _VirtualCardService_UseCard_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UseCardRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(VirtualCardServiceServer).UseCard(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/card.VirtualCardService/UseCard"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(VirtualCardServiceServer).UseCard(ctx, req.(*UseCardRequest))
	}
	return interceptor(ctx, in, info, handler)
}
