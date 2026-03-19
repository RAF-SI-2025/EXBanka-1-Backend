// Hand-written gRPC service descriptor for EmployeeLimitService.
// Since protoc is not available, this follows the same pattern as user_grpc.pb.go.
package userpb

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	EmployeeLimitService_GetEmployeeLimits_FullMethodName    = "/user.EmployeeLimitService/GetEmployeeLimits"
	EmployeeLimitService_SetEmployeeLimits_FullMethodName    = "/user.EmployeeLimitService/SetEmployeeLimits"
	EmployeeLimitService_ApplyLimitTemplate_FullMethodName   = "/user.EmployeeLimitService/ApplyLimitTemplate"
	EmployeeLimitService_ListLimitTemplates_FullMethodName   = "/user.EmployeeLimitService/ListLimitTemplates"
	EmployeeLimitService_CreateLimitTemplate_FullMethodName  = "/user.EmployeeLimitService/CreateLimitTemplate"
)

// EmployeeLimitServiceClient is the client API for EmployeeLimitService.
type EmployeeLimitServiceClient interface {
	GetEmployeeLimits(ctx context.Context, in *EmployeeLimitRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error)
	SetEmployeeLimits(ctx context.Context, in *SetEmployeeLimitsRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error)
	ApplyLimitTemplate(ctx context.Context, in *ApplyLimitTemplateRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error)
	ListLimitTemplates(ctx context.Context, in *ListLimitTemplatesRequest, opts ...grpc.CallOption) (*ListLimitTemplatesResponse, error)
	CreateLimitTemplate(ctx context.Context, in *CreateLimitTemplateRequest, opts ...grpc.CallOption) (*LimitTemplateResponse, error)
}

type employeeLimitServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewEmployeeLimitServiceClient(cc grpc.ClientConnInterface) EmployeeLimitServiceClient {
	return &employeeLimitServiceClient{cc}
}

func (c *employeeLimitServiceClient) GetEmployeeLimits(ctx context.Context, in *EmployeeLimitRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(EmployeeLimitResponse)
	err := c.cc.Invoke(ctx, EmployeeLimitService_GetEmployeeLimits_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *employeeLimitServiceClient) SetEmployeeLimits(ctx context.Context, in *SetEmployeeLimitsRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(EmployeeLimitResponse)
	err := c.cc.Invoke(ctx, EmployeeLimitService_SetEmployeeLimits_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *employeeLimitServiceClient) ApplyLimitTemplate(ctx context.Context, in *ApplyLimitTemplateRequest, opts ...grpc.CallOption) (*EmployeeLimitResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(EmployeeLimitResponse)
	err := c.cc.Invoke(ctx, EmployeeLimitService_ApplyLimitTemplate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *employeeLimitServiceClient) ListLimitTemplates(ctx context.Context, in *ListLimitTemplatesRequest, opts ...grpc.CallOption) (*ListLimitTemplatesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListLimitTemplatesResponse)
	err := c.cc.Invoke(ctx, EmployeeLimitService_ListLimitTemplates_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *employeeLimitServiceClient) CreateLimitTemplate(ctx context.Context, in *CreateLimitTemplateRequest, opts ...grpc.CallOption) (*LimitTemplateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(LimitTemplateResponse)
	err := c.cc.Invoke(ctx, EmployeeLimitService_CreateLimitTemplate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EmployeeLimitServiceServer is the server API for EmployeeLimitService.
type EmployeeLimitServiceServer interface {
	GetEmployeeLimits(context.Context, *EmployeeLimitRequest) (*EmployeeLimitResponse, error)
	SetEmployeeLimits(context.Context, *SetEmployeeLimitsRequest) (*EmployeeLimitResponse, error)
	ApplyLimitTemplate(context.Context, *ApplyLimitTemplateRequest) (*EmployeeLimitResponse, error)
	ListLimitTemplates(context.Context, *ListLimitTemplatesRequest) (*ListLimitTemplatesResponse, error)
	CreateLimitTemplate(context.Context, *CreateLimitTemplateRequest) (*LimitTemplateResponse, error)
	mustEmbedUnimplementedEmployeeLimitServiceServer()
}

// UnimplementedEmployeeLimitServiceServer must be embedded for forward compatibility.
type UnimplementedEmployeeLimitServiceServer struct{}

func (UnimplementedEmployeeLimitServiceServer) GetEmployeeLimits(context.Context, *EmployeeLimitRequest) (*EmployeeLimitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetEmployeeLimits not implemented")
}
func (UnimplementedEmployeeLimitServiceServer) SetEmployeeLimits(context.Context, *SetEmployeeLimitsRequest) (*EmployeeLimitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method SetEmployeeLimits not implemented")
}
func (UnimplementedEmployeeLimitServiceServer) ApplyLimitTemplate(context.Context, *ApplyLimitTemplateRequest) (*EmployeeLimitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ApplyLimitTemplate not implemented")
}
func (UnimplementedEmployeeLimitServiceServer) ListLimitTemplates(context.Context, *ListLimitTemplatesRequest) (*ListLimitTemplatesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListLimitTemplates not implemented")
}
func (UnimplementedEmployeeLimitServiceServer) CreateLimitTemplate(context.Context, *CreateLimitTemplateRequest) (*LimitTemplateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateLimitTemplate not implemented")
}
func (UnimplementedEmployeeLimitServiceServer) mustEmbedUnimplementedEmployeeLimitServiceServer() {}
func (UnimplementedEmployeeLimitServiceServer) testEmbeddedByValue()                              {}

// UnsafeEmployeeLimitServiceServer may be embedded to opt out of forward compatibility.
type UnsafeEmployeeLimitServiceServer interface {
	mustEmbedUnimplementedEmployeeLimitServiceServer()
}

func RegisterEmployeeLimitServiceServer(s grpc.ServiceRegistrar, srv EmployeeLimitServiceServer) {
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&EmployeeLimitService_ServiceDesc, srv)
}

func _EmployeeLimitService_GetEmployeeLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(EmployeeLimitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmployeeLimitServiceServer).GetEmployeeLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EmployeeLimitService_GetEmployeeLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmployeeLimitServiceServer).GetEmployeeLimits(ctx, req.(*EmployeeLimitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EmployeeLimitService_SetEmployeeLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SetEmployeeLimitsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmployeeLimitServiceServer).SetEmployeeLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EmployeeLimitService_SetEmployeeLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmployeeLimitServiceServer).SetEmployeeLimits(ctx, req.(*SetEmployeeLimitsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EmployeeLimitService_ApplyLimitTemplate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ApplyLimitTemplateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmployeeLimitServiceServer).ApplyLimitTemplate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EmployeeLimitService_ApplyLimitTemplate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmployeeLimitServiceServer).ApplyLimitTemplate(ctx, req.(*ApplyLimitTemplateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EmployeeLimitService_ListLimitTemplates_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListLimitTemplatesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmployeeLimitServiceServer).ListLimitTemplates(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EmployeeLimitService_ListLimitTemplates_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmployeeLimitServiceServer).ListLimitTemplates(ctx, req.(*ListLimitTemplatesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EmployeeLimitService_CreateLimitTemplate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateLimitTemplateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EmployeeLimitServiceServer).CreateLimitTemplate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: EmployeeLimitService_CreateLimitTemplate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EmployeeLimitServiceServer).CreateLimitTemplate(ctx, req.(*CreateLimitTemplateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// EmployeeLimitService_ServiceDesc is the grpc.ServiceDesc for EmployeeLimitService.
var EmployeeLimitService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "user.EmployeeLimitService",
	HandlerType: (*EmployeeLimitServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetEmployeeLimits",
			Handler:    _EmployeeLimitService_GetEmployeeLimits_Handler,
		},
		{
			MethodName: "SetEmployeeLimits",
			Handler:    _EmployeeLimitService_SetEmployeeLimits_Handler,
		},
		{
			MethodName: "ApplyLimitTemplate",
			Handler:    _EmployeeLimitService_ApplyLimitTemplate_Handler,
		},
		{
			MethodName: "ListLimitTemplates",
			Handler:    _EmployeeLimitService_ListLimitTemplates_Handler,
		},
		{
			MethodName: "CreateLimitTemplate",
			Handler:    _EmployeeLimitService_CreateLimitTemplate_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "user/limit.proto",
}
