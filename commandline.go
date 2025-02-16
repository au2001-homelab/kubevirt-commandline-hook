// Inspired by https://github.com/kubevirt/kubevirt/tree/main/cmd/example-disk-mutation-hook-sidecar

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/clbanning/mxj"
	"google.golang.org/grpc"

	vmSchema "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/hooks"
	hooksInfo "kubevirt.io/kubevirt/pkg/hooks/info"
	hooksV1alpha2 "kubevirt.io/kubevirt/pkg/hooks/v1alpha2"
	domainSchema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

type infoServer struct{}

func (s infoServer) Info(ctx context.Context, params *hooksInfo.InfoParams) (*hooksInfo.InfoResult, error) {
	return &hooksInfo.InfoResult{
		Name: "commandline",
		Versions: []string{
			hooksV1alpha2.Version,
		},
		HookPoints: []*hooksInfo.HookPoint{
			{
				Name:     hooksInfo.OnDefineDomainHookPointName,
				Priority: 0,
			},
		},
	}, nil
}

type v1alpha2Server struct{}

func (s v1alpha2Server) OnDefineDomain(ctx context.Context, params *hooksV1alpha2.OnDefineDomainParams) (*hooksV1alpha2.OnDefineDomainResult, error) {
	log.Log.Info("OnDefineDomain hook callback method has been called")

	vmiJSON := params.GetVmi()
	vmiSpec := vmSchema.VirtualMachineInstance{}
	err := json.Unmarshal(vmiJSON, &vmiSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given VMI spec: %s", vmiJSON)
		panic(err)
	}

	domainXML := params.GetDomainXML()
	domainSpec, err := mxj.NewMapXml(domainXML)
	if err != nil || domainSpec == nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given domain spec: %s", domainXML)
		panic(err)
	}

	argsSpec, err := domainSpec.ValuesForPath("domain.qemu:commandline.qemu:arg")
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to get values for path 'domain.qemu:commandline.qemu:arg': %+v", domainSpec)
		panic(err)
	}

	annotations := vmiSpec.GetAnnotations()
	for key, value := range annotations {
		if strings.HasPrefix(key, "arg.commandline.vm.kubevirt.io/") {
			arg := "-" + key[31:] + "=" + value

			argsSpec = append(argsSpec, mxj.Map{
				"-value": arg,
			})
		}
	}

	_, err = domainSpec.UpdateValuesForPath(mxj.Map{
		"qemu:arg": argsSpec,
	}, "domain.qemu:commandline.qemu:arg")
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to update values for path 'domain.qemu:commandline.qemu:arg': %+v", argsSpec)
		panic(err)
	}

	newDomainXML, err := domainSpec.Xml()
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to marshal updated domain spec: %+v", domainSpec)
		panic(err)
	}

	log.Log.Info("Successfully updated original domain spec with requested boot disk attribute")
	return &hooksV1alpha2.OnDefineDomainResult{
		DomainXML: newDomainXML,
	}, nil
}

func (s v1alpha2Server) PreCloudInitIso(_ context.Context, params *hooksV1alpha2.PreCloudInitIsoParams) (*hooksV1alpha2.PreCloudInitIsoResult, error) {
	log.Log.Info("PreCloudInitIso hook callback method has been called")

	return &hooksV1alpha2.PreCloudInitIsoResult{
		CloudInitData: params.GetCloudInitData(),
	}, nil
}

func main() {
	log.InitializeLogging("commandline-hook-sidecar")

	socketPath := filepath.Join(hooks.HookSocketsSharedDirectory, "commandline.sock")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to initialized socket on path: %s", socket)
		log.Log.Error("Check whether given directory exists and socket name is not already taken by other file")
		panic(err)
	}
	defer os.Remove(socketPath)

	server := grpc.NewServer([]grpc.ServerOption{}...)
	hooksInfo.RegisterInfoServer(server, infoServer{})
	hooksV1alpha2.RegisterCallbacksServer(server, v1alpha2Server{})
	log.Log.Infof("Starting hook server exposing 'info' and 'v1alpha2' services on socket %s", socketPath)
	server.Serve(socket)
}
