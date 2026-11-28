// Package mcp connects Gojent applications to Model Context Protocol servers.
//
// It is a client adapter. A Client launches stdio servers, negotiates a
// protocol revision, lists each server's tools, and exposes them as ordinary
// tools that implement github.com/ijaidev/gojent/core/tool. Gojent's policy and
// approver remain the final gate for every call.
//
// The adapter only connects to servers; it never serves MCP.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// ErrClientClosed reports use of a client after Close.
	ErrClientClosed = errors.New("mcp: client closed")
	// ErrAlreadyConnected reports a second Connect call. Reconnect support
	// replaces a session rather than adding another one.
	ErrAlreadyConnected = errors.New("mcp: client already connected")
	// ErrToolNameCollision reports two server tools whose names normalize to
	// the same Gojent tool name.
	ErrToolNameCollision = errors.New("mcp: tool name collision")
)

// Status is a server connection state.
type Status string

const (
	// StatusConnected means the session and its tool catalog are usable.
	StatusConnected Status = "connected"
	// StatusFailed means the server could not be connected or cataloged.
	StatusFailed Status = "failed"
)

// ServerConfig configures one stdio MCP server.
type ServerConfig struct {
	// Name identifies the server. It is required and unique, and it prefixes
	// the server's tool names.
	Name string
	// Command is the executable to launch. It is resolved with exec.LookPath.
	Command string
	// Args are the command arguments.
	Args []string
	// Dir is the child working directory. Empty uses the current process
	// working directory.
	Dir string
	// Env is the complete environment allowlist for the child, in KEY=VALUE
	// form. The child also receives a small platform minimum; it inherits no
	// other host variables.
	Env []string
}

// ServerStatus reports one server's connection result.
type ServerStatus struct {
	// Name is the configured server name.
	Name string
	// Status is connected or failed.
	Status Status
	// ProtocolVersion is the negotiated MCP revision when connected.
	ProtocolVersion string
	// Err is the connection or catalog error when Status is failed. It never
	// includes environment values.
	Err error
}

// session is the subset of an SDK client session used by the adapter. The SDK
// session satisfies it; tests supply in-memory sessions.
type session interface {
	ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
	InitializeResult() *sdk.InitializeResult
	Close() error
}

type server struct {
	config  ServerConfig
	session session
	tools   []*sdk.Tool
}

// Client owns MCP server sessions and their tool catalogs.
//
// Connect must be called once before Tools or any returned tool. Tools and
// Execute are safe for concurrent use; Close is safe to call concurrently
// with them and is idempotent.
type Client struct {
	dial func(context.Context, ServerConfig) (session, error)

	mu        sync.RWMutex
	servers   []*server
	byName    map[string]*server
	connected bool
	closed    bool
}

// New creates a client that launches stdio servers.
func New() *Client {
	return newClient(dialStdio)
}

func newClient(dial func(context.Context, ServerConfig) (session, error)) *Client {
	return &Client{
		dial:   dial,
		byName: make(map[string]*server),
	}
}

// Connect starts every configured server independently and lists its tools.
// One server failing does not prevent the others from connecting. The returned
// statuses follow the config order; a per-server failure is reported on its
// status instead of as the function error.
//
// Connect calls dial and list requests with ctx. It may be called only once;
// reconnect is a separate operation.
func (c *Client) Connect(ctx context.Context, configs []ServerConfig) ([]ServerStatus, error) {
	if c == nil {
		return nil, ErrClientClosed
	}
	if err := validateConfigs(configs); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	if c.connected {
		c.mu.Unlock()
		return nil, ErrAlreadyConnected
	}
	c.connected = true
	c.mu.Unlock()

	statuses := make([]ServerStatus, 0, len(configs))
	for _, config := range configs {
		status := ServerStatus{Name: config.Name}
		sess, err := c.dial(ctx, config)
		if err == nil {
			var tools []*sdk.Tool
			tools, err = listTools(ctx, sess)
			if err == nil {
				version := protocolVersion(sess)
				err = c.addServer(&server{
					config:  cloneConfig(config),
					session: sess,
					tools:   tools,
				})
				if err == nil {
					status.Status = StatusConnected
					status.ProtocolVersion = version
				}
			}
			if err != nil {
				_ = sess.Close()
			}
		}
		if err != nil {
			status.Status = StatusFailed
			status.Err = err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (c *Client) addServer(srv *server) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClientClosed
	}
	c.servers = append(c.servers, srv)
	c.byName[srv.config.Name] = srv
	return nil
}

// Tools returns the connected servers' tools as Gojent tools, named
// <server>_<tool>. It fails before any model call when two tools normalize to
// the same name.
func (c *Client) Tools() ([]tool.Tool, error) {
	if c == nil {
		return nil, ErrClientClosed
	}

	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, ErrClientClosed
	}
	servers := append([]*server(nil), c.servers...)
	c.mu.RUnlock()

	type source struct {
		server string
		tool   string
	}
	seen := make(map[string]source)
	var tools []tool.Tool
	for _, srv := range servers {
		for _, definition := range srv.tools {
			name, err := toolName(srv.config.Name, definition.Name)
			if err != nil {
				return nil, fmt.Errorf("mcp: server %q: %w", srv.config.Name, err)
			}
			if previous, ok := seen[name]; ok {
				return nil, fmt.Errorf(
					"%w: %q is produced by server %q tool %q and server %q tool %q",
					ErrToolNameCollision, name,
					previous.server, previous.tool, srv.config.Name, definition.Name,
				)
			}
			seen[name] = source{server: srv.config.Name, tool: definition.Name}

			schema, err := normalizeSchema(definition.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("mcp: server %q tool %q: %w", srv.config.Name, definition.Name, err)
			}
			tools = append(tools, &serverTool{
				client:     c,
				server:     srv.config.Name,
				name:       name,
				definition: definition,
				schema:     schema,
			})
		}
	}
	return tools, nil
}

// Close closes every server session and waits for its subprocess to exit. It
// is idempotent and joins per-server close errors.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	servers := c.servers
	c.servers = nil
	c.byName = nil
	c.mu.Unlock()

	var errs []error
	for _, srv := range servers {
		if err := srv.session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp: close server %q: %w", srv.config.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Client) session(name string) (session, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrClientClosed
	}
	srv, ok := c.byName[name]
	if !ok || srv.session == nil {
		return nil, fmt.Errorf("mcp: server %q is not connected", name)
	}
	return srv.session, nil
}

func dialStdio(ctx context.Context, config ServerConfig) (session, error) {
	env, err := buildEnv(config.Env)
	if err != nil {
		return nil, err
	}
	command := exec.Command(config.Command, config.Args...)
	command.Dir = config.Dir
	command.Env = env

	client := sdk.NewClient(
		&sdk.Implementation{Name: "gojent", Version: "dev"},
		&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}},
	)
	sess, err := client.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %q: %w", config.Name, err)
	}
	return sess, nil
}

func listTools(ctx context.Context, sess session) ([]*sdk.Tool, error) {
	var tools []*sdk.Tool
	seen := make(map[string]struct{})
	cursor := ""
	for {
		result, err := sess.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("mcp: list tools: %w", ctxErr)
			}
			return nil, fmt.Errorf("mcp: list tools: %w", err)
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		if _, duplicate := seen[result.NextCursor]; duplicate {
			return nil, fmt.Errorf("mcp: list tools: server repeated cursor %q", result.NextCursor)
		}
		seen[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
}

func protocolVersion(sess session) string {
	if result := sess.InitializeResult(); result != nil {
		return result.ProtocolVersion
	}
	return ""
}

func validateConfigs(configs []ServerConfig) error {
	seen := make(map[string]string, len(configs))
	for index, config := range configs {
		if strings.TrimSpace(config.Name) == "" {
			return fmt.Errorf("mcp: server config %d: name is required", index)
		}
		normalized := normalizeSegment(config.Name)
		if normalized == "" {
			return fmt.Errorf("mcp: server config %d: name %q has no usable ASCII characters", index, config.Name)
		}
		if previous, ok := seen[normalized]; ok {
			return fmt.Errorf("mcp: server names %q and %q normalize to the same prefix %q", previous, config.Name, normalized)
		}
		seen[normalized] = config.Name

		if strings.TrimSpace(config.Command) == "" {
			return fmt.Errorf("mcp: server %q: command is required", config.Name)
		}
		if _, err := buildEnv(config.Env); err != nil {
			return fmt.Errorf("mcp: server %q: %w", config.Name, err)
		}
	}
	return nil
}

func cloneConfig(config ServerConfig) ServerConfig {
	clone := config
	clone.Args = append([]string(nil), config.Args...)
	clone.Env = append([]string(nil), config.Env...)
	return clone
}

// buildEnv combines a platform minimum with the configured allowlist. The
// result is sorted so children observe a stable environment.
func buildEnv(allowlist []string) ([]string, error) {
	environment := make(map[string]string)
	for _, name := range platformEnvNames() {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	for index, entry := range allowlist {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("environment entry %d: want KEY=VALUE", index)
		}
		environment[name] = value
	}

	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)

	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+environment[name])
	}
	return env, nil
}

func platformEnvNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"COMSPEC", "PATHEXT", "SYSTEMROOT", "TEMP", "TMP", "USERPROFILE", "WINDIR"}
	}
	return []string{"HOME", "LANG", "LC_ALL", "PATH", "TMPDIR"}
}
