package podman

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/openshift/check-payload/internal/types"
	"k8s.io/klog/v2"
)

func Unmount(ctx context.Context, id string) error {
	_, err := runPodman(ctx, "image", "unmount", id)
	if err != nil {
		return err
	}
	return nil
}

func Mount(ctx context.Context, id string) (string, error) {
	stdout, err := runPodman(ctx, "image", "mount", id)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func Pull(ctx context.Context, image string, insecure bool) error {
	args := []string{"pull"}
	if insecure {
		args = append(args, "--tls-verify=false")
	}
	args = append(args, image)

	_, err := runPodman(ctx, args...)
	if err != nil {
		return err
	}
	return nil
}

func Inspect(ctx context.Context, image string, args ...string) (string, error) {
	cmdArgs := append([]string{"inspect", image}, args...)
	stdout, err := runPodman(ctx, cmdArgs...)
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func runPodman(ctx context.Context, args ...string) (bytes.Buffer, error) {
	klog.V(1).InfoS("podman "+args[0], "args", args[1:])
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout, fmt.Errorf("podman error (args=%v) (stderr=%v) (error=%w)", args, stderr.String(), err)
	}
	return stdout, nil
}

func GetOpenshiftComponentFromImage(ctx context.Context, image string) (*types.OpenshiftComponent, error) {
	data, err := Inspect(ctx, image, "--format", "{{index  .Config.Labels \"com.redhat.component\" }}|{{index  .Config.Labels \"io.openshift.build.source-location\" }}|{{index .Config.Labels \"io.openshift.maintainer.component\"}}|{{index .Config.Labels \"com.redhat.delivery.operator.bundle\"}}")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(data, "|")

	oc := &types.OpenshiftComponent{}
	oc.Component = strings.TrimSpace(parts[0])
	oc.SourceLocation = strings.TrimSpace(parts[1])
	oc.MaintainerComponent = strings.TrimSpace(parts[2])
	oc.IsBundle = strings.EqualFold(strings.TrimSpace(parts[3]), "true")
	return oc, nil
}
