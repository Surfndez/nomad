package taskrunner

import (
	"context"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-version"
	ifs "github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/client/consul"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/pkg/errors"
)

const (
	// envoyVersionHookName is the name of this hook and appears in logs.
	envoyVersionHookName = "envoy_version"

	// envoyLegacyImage is used when the version of Consul is too old to support
	// the SupportedProxies field in the self API.
	//
	// This is the version defaulted by Nomad before v0.13.0 and/or when using versions
	// of Consul before v1.7.8, v1.8.5, and v1.9.0.
	envoyLegacyImage = "envoyproxy/envoy:v1.11.2@sha256:a7769160c9c1a55bb8d07a3b71ce5d64f72b1f665f10d81aa1581bc3cf850d09"
)

type envoyVersionHookConfig struct {
	alloc         *structs.Allocation
	proxiesClient consul.SupportedProxiesAPI
	logger        hclog.Logger
}

func newEnvoyVersionHookConfig(alloc *structs.Allocation, proxiesClient consul.SupportedProxiesAPI, logger hclog.Logger) *envoyVersionHookConfig {
	return &envoyVersionHookConfig{
		alloc:         alloc,
		logger:        logger,
		proxiesClient: proxiesClient,
	}
}

type envoyVersionHook struct {
	// alloc is the allocation with the envoy task being rewritten.
	alloc *structs.Allocation

	// proxiesClient is the subset of the Consul API for getting information
	// from Consul about the versions of Envoy it supports.
	proxiesClient consul.SupportedProxiesAPI

	// logger is used to log things.
	logger hclog.Logger
}

func newEnvoyVersionHook(c *envoyVersionHookConfig) *envoyVersionHook {
	return &envoyVersionHook{
		alloc:         c.alloc,
		proxiesClient: c.proxiesClient,
		logger:        c.logger.Named(envoyVersionHookName),
	}
}

func (envoyVersionHook) Name() string {
	return envoyVersionHookName
}

func (h *envoyVersionHook) Prestart(_ context.Context, request *ifs.TaskPrestartRequest, response *ifs.TaskPrestartResponse) error {
	if h.skip(request) {
		response.Done = true
		return nil
	}

	// it's either legacy or manageable, need to know consul version
	proxies, err := h.proxiesClient.Proxies()
	if err != nil {
		return err
	}

	image, err := h.image(proxies)
	if err != nil {
		return err
	}

	h.logger.Trace("setting task envoy image", "image", image)
	request.Task.Config["image"] = image
	response.Done = true
	return nil
}

// skip will return true if the request does not contain a task that should have
// its envoy proxy version resolved automatically.
func (h *envoyVersionHook) skip(request *ifs.TaskPrestartRequest) bool {
	switch {
	case request.Task.Driver != "docker":
		return true
	case !request.Task.UsesConnectSidecar():
		return true
	case !h.needsVersion(request.Task.Config):
		return true
	}
	return false
}

// needsVersion returns true if the docker.config.image is making use of the fake
// ${NOMAD_envoy_version}
// Nomad does not need to query Consul to get the preferred Envoy version, etc.)
func (_ *envoyVersionHook) needsVersion(config map[string]interface{}) bool {
	if len(config) == 0 {
		return false
	}

	image, ok := config["image"].(string)
	if !ok {
		return false
	}

	return strings.Contains(image, structs.EnvoyVersionVar)
}

// image determines the best Envoy version to use. If supported is nil or empty
// Nomad will fallback to the legacy envoy image used before Nomad v0.13.
func (_ *envoyVersionHook) image(supported map[string][]string) (string, error) {
	versions := supported["envoy"]
	if len(versions) == 0 {
		return envoyLegacyImage, nil
	}

	latest, err := semver(versions[0])
	if err != nil {
		return "", err
	}

	return strings.ReplaceAll(structs.EnvoyImageFormat, structs.EnvoyVersionVar, latest), nil
}

// semver sanitizes the envoy version string coming from Consul into the format
// used by the Envoy project when publishing images (i.e. proper semver). This
// resulting string value does NOT contain the 'v' prefix for 2 reasons:
// 1) the version library does not include the 'v'
// 2) its plausible unofficial images use the 3 numbers without the prefix for
//    tagging their own images
func semver(chosen string) (string, error) {
	v, err := version.NewVersion(chosen)
	if err != nil {
		return "", errors.Wrap(err, "unexpected envoy version format")
	}
	return v.String(), nil
}
