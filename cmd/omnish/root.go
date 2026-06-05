package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "omnish",
		Short:   "Multi-protocol interactive shell daemon",
		Version: buildVersion,
		Long: `omnish — multi-protocol interactive shell daemon

omnish starts a single process that simultaneously exposes the same command
shell over Telnet, SSH, and local stdio, while also acting as a JSON-RPC 2.0
server and a Modbus TCP/RTU slave.  All services share one command registry,
so anything you register is instantly reachable from every protocol.

DEFAULT PORTS

  Telnet shell   :2323   →  telnet localhost 2323
  SSH shell      :2222   →  ssh -p 2222 -o StrictHostKeyChecking=no localhost
  JSON-RPC 2.0   :9000   →  nc localhost 9000
  Modbus TCP     :502    →  any standard Modbus TCP client

QUICK START

  1. Build and run with every service enabled:

       make build
       ./omnish serve

  2. In another terminal, try the shell over Telnet:

       telnet localhost 2323
       > help
       > status
       > quit

  3. Try JSON-RPC over netcat:

       echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' | nc localhost 9000

  4. Stop the daemon with Ctrl-C (or SIGTERM).

Run 'omnish serve --help' for the full list of flags.`,
	}

	root.AddCommand(newServeCmd())
	root.AddCommand(newVersionCmd())
	return root
}
