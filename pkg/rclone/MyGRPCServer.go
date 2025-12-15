package rclone

import (
	"context"
	"net"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"google.golang.org/grpc"
)

// Override the serve function to keep the csi socket instead of removing it before use.
// this is basically a copy-paste of csi-common/server.go with two lines modified.
// done as a quick hack and as the actual implementation struct is private, so I can't ovveride the `server`function`
// only.

func NewMyGRPCServer() csicommon.NonBlockingGRPCServer {
	return &myGRPCServer{}
}

type myGRPCServer struct {
	wg     sync.WaitGroup
	server *grpc.Server
}

func (s *myGRPCServer) Start(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {

	s.wg.Add(1)

	go s.serve(endpoint, ids, cs, ns)

	return
}

func (s *myGRPCServer) Wait() {
	s.wg.Wait()
}

func (s *myGRPCServer) Stop() {
	s.server.GracefulStop()
}

func (s *myGRPCServer) ForceStop() {
	s.server.Stop()
}

func logGRPC(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	glog.V(3).Infof("GRPC call: %s", info.FullMethod)
	glog.V(5).Infof("GRPC request: %s", protosanitizer.StripSecrets(req))
	resp, err := handler(ctx, req)
	if err != nil {
		glog.Errorf("GRPC error: %v", err)
	} else {
		glog.V(5).Infof("GRPC response: %s", protosanitizer.StripSecrets(resp))
	}
	return resp, err
}

func (s *myGRPCServer) serve(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {

	proto, addr, err := csicommon.ParseEndpoint(endpoint)
	if err != nil {
		glog.Fatal(err.Error())
	}

	if proto == "unix" {
		addr = "/" + addr
		//if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		//	glog.Fatalf("Failed to remove %s, error: %s", addr, err.Error())
		//}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		glog.Fatalf("Failed to listen: %v", err)
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logGRPC),
	}
	server := grpc.NewServer(opts...)
	s.server = server

	if ids != nil {
		csi.RegisterIdentityServer(server, ids)
	}
	if cs != nil {
		csi.RegisterControllerServer(server, cs)
	}
	if ns != nil {
		csi.RegisterNodeServer(server, ns)
	}

	glog.Infof("Listening for connections on address: %#v", listener.Addr())

	server.Serve(listener)

}
