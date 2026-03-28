# netconf — NETCONF protocol library for Go

A pure-Go implementation of the NETCONF management protocol, covering:

- **RFC 6241** — NETCONF base protocol, all 13 operations
- **RFC 6242** — SSH transport with both EOM (`]]>]]>`) and chunked framing
- **RFC 7803** — capability URN validation

## Packages

| Import path | Description |
|-------------|-------------|
| `github.com/GabrielNunesIT/netconf` | Core protocol types: Hello, RPC, RPCReply messages; session management; capability negotiation; operation structs for all 13 RFC 6241 operations; error model |
| `github.com/GabrielNunesIT/netconf/client` | NETCONF client with typed methods for all 13 operations, concurrent RPC dispatch, context support |
| `github.com/GabrielNunesIT/netconf/server` | NETCONF server with handler registration, RPC dispatch loop, built-in close-session handling |
| `github.com/GabrielNunesIT/netconf/transport` | Transport interface, EOM and chunked framers, loopback transport for testing |
| `github.com/GabrielNunesIT/netconf/transport/ssh` | SSH client and server transports using `golang.org/x/crypto/ssh` |

## Requirements

- **Go 1.26** or later

## Installation

```sh
go get github.com/GabrielNunesIT/netconf
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	gossh "golang.org/x/crypto/ssh"
)

func main() {
	sshCfg := &gossh.ClientConfig{
		User: "admin",
		Auth: []gossh.AuthMethod{
			gossh.Password("secret"),
		},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	ctx := context.Background()

	localCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	c, err := client.Dial(ctx, "router.example.com:830", sshCfg, localCaps)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	data, err := c.GetConfig(ctx, netconf.Running, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("running config:\n%s\n", data.Body)

	if err := c.CloseSession(ctx); err != nil {
		log.Fatal(err)
	}
}
```

## Testing

Run the full test suite — unit tests across all packages plus the RFC 6241 conformance tests:

```sh
go test ./...
```

Run only the conformance tests with verbose output:

```sh
go test ./netconf/conformance/... -v -count=1
```

Run static analysis:

```sh
go vet ./...
```

## License

MIT — see [LICENSE](LICENSE) (placeholder; add LICENSE file before publishing).
