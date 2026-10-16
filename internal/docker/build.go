package docker

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
)

// loadTLS reads client TLS material for a remote Docker daemon.
func loadTLS(caPath, certPath, keyPath string) (*tls.Config, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no certificates found in %s", caPath)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// BuildRequest describes an image build.
type BuildRequest struct {
	// ContextDir is the local directory sent to the engine as the build context.
	ContextDir string
	// Dockerfile is relative to ContextDir.
	Dockerfile string
	Tags       []string
	BuildArgs  map[string]*string
	Labels     map[string]string
	Target     string
	NoCache    bool
	Pull       bool
}

// Build runs an image build and forwards progress to sink as plain text.
//
// The context is streamed as a tar archive, so the daemon never needs direct
// filesystem access and the same call works over an SSH tunnel.
func (c *Client) Build(ctx context.Context, req BuildRequest, sink io.Writer) (*BuildResult, error) {
	if sink == nil {
		sink = io.Discard
	}

	contextReader, err := tarContext(req.ContextDir)
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}
	defer contextReader.Close()

	options := build.ImageBuildOptions{
		Tags:       req.Tags,
		Dockerfile: valueOrDefault(req.Dockerfile, "Dockerfile"),
		BuildArgs:  req.BuildArgs,
		Labels:     req.Labels,
		Target:     req.Target,
		NoCache:    req.NoCache,
		PullParent: req.Pull,
		Remove:     true,
		Version:    build.BuilderBuildKit,
	}

	resp, err := c.cli.ImageBuild(ctx, contextReader, options)
	if err != nil {
		return nil, fmt.Errorf("start build: %w", err)
	}
	defer resp.Body.Close()

	result, err := readBuildStream(resp.Body, sink)
	if err != nil {
		return nil, fmt.Errorf("build stream: %w", err)
	}
	return result, nil
}

// BuildResult reports the outcome of a build.
type BuildResult struct {
	ImageID string
}

// buildFrame is one line of the engine's build progress output.
type buildFrame struct {
	Stream      string `json:"stream"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error string          `json:"error"`
	Aux   json.RawMessage `json:"aux"`
}

// readBuildStream decodes the engine's newline-delimited JSON, writes human
// readable progress to sink and returns the built image id.
func readBuildStream(body io.Reader, sink io.Writer) (*BuildResult, error) {
	out := &BuildResult{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var frame buildFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			// Some engine versions emit raw text. Pass it through rather than
			// failing a build that is otherwise healthy.
			sink.Write(append(line, '\n'))
			continue
		}

		if frame.Error != "" || frame.ErrorDetail.Message != "" {
			message := frame.Error
			if message == "" {
				message = frame.ErrorDetail.Message
			}
			sink.Write([]byte("[x] " + message + "\n"))
			return out, fmt.Errorf("build failed: %s", message)
		}

		if frame.Stream != "" {
			sink.Write([]byte(frame.Stream))
			continue
		}

		if len(frame.Aux) > 0 {
			var aux struct {
				ID string `json:"ID"`
			}
			if json.Unmarshal(frame.Aux, &aux) == nil && aux.ID != "" {
				out.ImageID = aux.ID
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func valueOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// TagImage applies an additional tag to an existing image.
func (c *Client) TagImage(ctx context.Context, source, target string) error {
	if err := c.cli.ImageTag(ctx, source, target); err != nil {
		return fmt.Errorf("tag %s as %s: %w", source, target, err)
	}
	return nil
}

// PushImage pushes a tagged image and streams progress to sink.
func (c *Client) PushImage(ctx context.Context, ref, auth string, sink io.Writer) error {
	if sink == nil {
		sink = io.Discard
	}
	reader, err := c.cli.ImagePush(ctx, ref, image.PushOptions{RegistryAuth: auth})
	if err != nil {
		return fmt.Errorf("push %s: %w", ref, err)
	}
	defer reader.Close()

	// Push output uses the same newline-delimited format as build progress.
	if _, err := readBuildStream(reader, sink); err != nil {
		return err
	}
	return nil
}

// ContainersPrune removes stopped containers. Returns bytes reclaimed.
func (c *Client) ContainersPrune(ctx context.Context) (int64, error) {
	report, err := c.cli.ContainersPrune(ctx, filters.NewArgs())
	if err != nil {
		return 0, fmt.Errorf("prune containers: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// BuildCachePrune clears the BuildKit cache. Returns bytes reclaimed.
func (c *Client) BuildCachePrune(ctx context.Context) (int64, error) {
	report, err := c.cli.BuildCachePrune(ctx, build.CachePruneOptions{All: true})
	if err != nil {
		return 0, fmt.Errorf("prune build cache: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// tarContext streams a directory as a tar archive suitable for a build context.
// It honours .dockerignore so node_modules and .git never reach the daemon.
func tarContext(root string) (io.ReadCloser, error) {
	if root == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	ignore, err := loadDockerIgnore(filepath.Join(root, ".dockerignore"))
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		walkErr := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if ignore.matches(rel, fi.IsDir()) {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				// Symlinks are skipped: the daemon cannot resolve them anyway.
				return nil
			}
			header, hErr := tar.FileInfoHeader(fi, "")
			if hErr != nil {
				return hErr
			}
			header.Name = rel
			if fi.IsDir() {
				header.Name += "/"
			}
			if wErr := tw.WriteHeader(header); wErr != nil {
				return wErr
			}
			if !fi.Mode().IsRegular() {
				return nil
			}
			file, oErr := os.Open(path)
			if oErr != nil {
				return oErr
			}
			defer file.Close()
			_, cErr := io.Copy(tw, file)
			return cErr
		})
		if walkErr == nil {
			walkErr = tw.Close()
		} else {
			tw.Close()
		}
		pw.CloseWithError(walkErr)
	}()
	return pr, nil
}

// ignoreList is a minimal .dockerignore matcher supporting *, ** and negation.
type ignoreList struct {
	patterns []string
	negated  []string
}

func loadDockerIgnore(path string) (*ignoreList, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ignoreList{patterns: []string{".git", "node_modules", ".dockerignore"}}, nil
		}
		return nil, err
	}

	out := &ignoreList{}
	for _, line := range strings.Split(string(raw), "\n") {
		pattern := strings.TrimSpace(line)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if strings.HasPrefix(pattern, "!") {
			out.negated = append(out.negated, strings.TrimPrefix(pattern, "!"))
			continue
		}
		out.patterns = append(out.patterns, strings.TrimPrefix(pattern, "/"))
	}
	out.patterns = append(out.patterns, ".git", ".dockerignore")
	return out, nil
}

func (l *ignoreList) matches(rel string, isDir bool) bool {
	for _, n := range l.negated {
		if matchPattern(n, rel, isDir) {
			return false
		}
	}
	for _, p := range l.patterns {
		if matchPattern(p, rel, isDir) {
			return true
		}
	}
	return false
}

func matchPattern(pattern, rel string, isDir bool) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == rel {
		return true
	}
	// A directory pattern also excludes everything beneath it.
	if strings.HasPrefix(rel, pattern+"/") {
		return true
	}
	ok, err := filepath.Match(pattern, rel)
	if err != nil {
		return false
	}
	if ok {
		return true
	}
	// Support "**" anywhere in the pattern.
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, rel)
	}
	// A bare pattern like "*.log" should match nested files too.
	if !strings.Contains(pattern, "/") {
		ok, err = filepath.Match(pattern, filepath.Base(rel))
		return err == nil && ok
	}
	// Support "**" anywhere in the pattern.
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, rel)
	}
	return false
}

// matchDoubleStar resolves patterns such as "dist/**/*.js".
func matchDoubleStar(pattern, rel string) bool {
	parts := strings.Split(pattern, "**")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(rel[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			// Anchored at the start of the path.
			return false
		}
		pos += idx + len(part)
	}
	return true
}
