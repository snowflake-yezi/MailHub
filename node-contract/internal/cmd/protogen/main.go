package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type versions struct {
	Protoc          string `json:"protoc"`
	ProtocGenGo     string `json:"protoc_gen_go"`
	ProtocGenGoGRPC string `json:"protoc_gen_go_grpc"`
}

func main() {
	want, err := loadVersions("tools/versions.json")
	if err != nil {
		fatal(err)
	}

	checks := []struct {
		name string
		arg  string
		want string
	}{
		{name: "protoc", arg: "--version", want: want.Protoc},
		{name: "protoc-gen-go", arg: "--version", want: want.ProtocGenGo},
		{name: "protoc-gen-go-grpc", arg: "--version", want: want.ProtocGenGoGRPC},
	}
	for _, check := range checks {
		if err := requireVersion(check.name, check.arg, check.want); err != nil {
			fatal(err)
		}
	}
	if err := os.MkdirAll("gen", 0755); err != nil {
		fatal(fmt.Errorf("create generated output directory: %w", err))
	}

	cmd := exec.Command(
		"protoc",
		"--proto_path=proto",
		"--go_out=gen",
		"--go_opt=paths=source_relative",
		"--go-grpc_out=gen",
		"--go-grpc_opt=paths=source_relative",
		"proto/mailhub/node/v1/node.proto",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("generate node protocol: %w", err))
	}
}

func loadVersions(path string) (versions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return versions{}, fmt.Errorf("read tool versions: %w", err)
	}
	var value versions
	if err := json.Unmarshal(data, &value); err != nil {
		return versions{}, fmt.Errorf("parse tool versions: %w", err)
	}
	return value, nil
}

func requireVersion(name, arg, want string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s %s is required; see node-contract/README.md", name, want)
	}
	output, err := exec.Command(path, arg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("read %s version: %w", name, err)
	}
	got := strings.TrimSpace(string(output))
	if !strings.HasSuffix(got, " "+want) && !strings.HasSuffix(got, " v"+want) {
		return fmt.Errorf("%s version mismatch: got %q, want %s", name, got, want)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
