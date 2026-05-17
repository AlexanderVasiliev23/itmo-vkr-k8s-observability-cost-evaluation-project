package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/moby/go-archive"
)

type IDockerProvider interface {
	RecreateImageWithNewTag(ctx context.Context, tag, contextPath string) error
}

type provider struct {
}

func NewDockerProvider() IDockerProvider {
	return &provider{}
}

func (p *provider) RecreateImageWithNewTag(ctx context.Context, tag, contextPath string) error {
	if err := p.removeImage(ctx, tag); err != nil {
		return err
	}

	return p.buildImage(ctx, tag, contextPath)
}

func (p *provider) buildImage(ctx context.Context, tag, contextPath string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	buildCtx, err := archive.TarWithOptions(contextPath, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	defer buildCtx.Close()

	resp, err := cli.ImageBuild(ctx, buildCtx, dockerbuild.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("image build: %w", err)
	}
	defer resp.Body.Close()

	return printBuildOutput(resp.Body)
}

func (p *provider) removeImage(ctx context.Context, tag string) error {
	nameAndTag := strings.Split(tag, ":")
	if len(nameAndTag) != 2 {
		return fmt.Errorf("invalid image tag: %s", tag)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	list, err := cli.ImageList(ctx, image.ListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", nameAndTag[0])),
	})
	if err != nil {
		return fmt.Errorf("image list: %w", err)
	}

	for _, img := range list {
		if _, err := cli.ImageRemove(ctx, img.ID, image.RemoveOptions{
			Force: true,
		}); err != nil {
			return fmt.Errorf("remove image: %w", err)
		}
	}

	return nil
}

func printBuildOutput(r io.Reader) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("docker build: %s", msg.Error)
		}
	}
	return nil
}
