package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"

	"github.com/golang/glog"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jeongukjae/faiss-server/faiss"
	gw "github.com/jeongukjae/faiss-server/protos/faiss/service"
)

var (
	// command-line options:
	// gRPC server endpoint
	grpcServerEndpoint = flag.String("grpc_server_endpoint", "localhost:8000", "gRPC server endpoint")
	// http server endpoint
	httpServerEndpoint = flag.String("http_server_endpoint", "localhost:8001", "http server endpoint")
	// faiss index path
	faissIndexPath = flag.String("faiss_index", "", "path of prebuilt faiss index")
)

var index *faiss.FaissIndex

type faissServer struct {
	gw.UnimplementedFaissServer
}

func (s *faissServer) GetMetadata(ctx context.Context, in *gw.EmptyMessage) (*gw.GetMetadataResponse, error) {
	return &gw.GetMetadataResponse{
		IndexName:  index.Path,
		Dimension:  index.Dimension,
		MetricType: gw.GetMetadataResponse_MetricType(index.MetricType),
		Ntotal:     index.GetNtotal(),
	}, nil
}

func (s *faissServer) Search(ctx context.Context, in *gw.SearchRequest) (*gw.SearchResponse, error) {
	numElements := int32(len(in.Vectors))
	if numElements != in.NumVectors*index.Dimension {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"num elements of vector(%d) != num vectors(%d) * dimension(%d)",
			numElements, in.NumVectors, index.Dimension,
		)
	}

	results := index.Search(in.NumVectors, in.Vectors, in.TopK)
	return &gw.SearchResponse{
		Ids:       results.Ids,
		Distances: results.Distances,
	}, nil
}

func runGrpcServer() error {
	lis, err := net.Listen("tcp", *grpcServerEndpoint)
	if err != nil {
		return errors.WithStack(err)
	}

	s := grpc.NewServer()
	gw.RegisterFaissServer(s, &faissServer{})
	go func() { log.Fatalln(s.Serve(lis)) }()
	glog.Info("Serve grpc server at ", *grpcServerEndpoint)

	return nil
}

func runGrpcGateway() error {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register gRPC server endpoint
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithInsecure()}
	glog.Info("Register grpc gateway server at ", *grpcServerEndpoint)
	err := gw.RegisterFaissHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts)
	if err != nil {
		return err
	}

	// Start HTTP server (and proxy calls to gRPC server endpoint)
	glog.Info("Serve http server at ", *httpServerEndpoint)
	return http.ListenAndServe(*httpServerEndpoint, mux)
}

func main() {
	flag.Parse()
	defer glog.Flush()

	if *faissIndexPath == "" {
		glog.Fatal("You should pass faiss index path (-faiss_index option)")
	}

	var err error
	glog.Info("Loading faiss index from ", *faissIndexPath)
	index, err = faiss.LoadIndex(*faissIndexPath)
	if err != nil {
		glog.Fatal(err)
	}
	defer index.Free()

	if err = runGrpcServer(); err != nil {
		glog.Fatal(err)
	}

	if err = runGrpcGateway(); err != nil {
		glog.Fatal(err)
	}
}
