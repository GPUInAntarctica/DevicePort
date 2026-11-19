// Command deviceportctl is a tiny CLI for the local DevicePort HTTP API.
//
// Examples:
//
//	deviceportctl status
//	deviceportctl connect <32-hex-node-id>
//	deviceportctl send <virtual-ip> "hello"
//	deviceportctl -admin http://127.0.0.1:9001 networks
//	deviceportctl -admin http://127.0.0.1:9001 auth <id> on
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	api := flag.String("api", "http://127.0.0.1:9050", "node GUI/API base URL")
	admin := flag.String("admin", "http://127.0.0.1:9001", "server admin API base URL")
	flag.Parse()

	client := &http.Client{Timeout: 5 * time.Second}
	args := flag.Args()
	if len(args) == 0 {
		usage()
	}
	switch args[0] {
	case "status":
		getJSON(client, *api+"/api/status")
	case "connect":
		if len(args) < 2 {
			usage()
		}
		postForm(client, *api+"/api/connect", url.Values{"id": {args[1]}})
	case "send":
		if len(args) < 3 {
			usage()
		}
		pkt, err := buildEcho(args[1], strings.Join(args[2:], " "))
		if err != nil {
			fatal(err)
		}
		postForm(client, *api+"/api/send", url.Values{"hex": {pkt}})
	case "networks":
		getJSON(client, *admin+"/api/networks")
	case "members":
		net := "default"
		if len(args) > 1 {
			net = args[1]
		}
		getJSON(client, *admin+"/api/networks/"+net)
	case "auth":
		if len(args) < 3 {
			usage()
		}
		net := "default"
		id := args[1]
		allow := args[2]
		if len(args) > 3 {
			net, id, allow = args[1], args[2], args[3]
		}
		postForm(client, *admin+"/api/networks/"+net+"/auth", url.Values{"id": {id}, "allow": {allow}})
	case "require-auth":
		if len(args) < 3 {
			usage()
		}
		postForm(client, *admin+"/api/networks/"+args[1]+"/require-auth", url.Values{"value": {args[2]}})
	case "route":
		if len(args) < 4 {
			usage()
		}
		op, net, cidr := args[1], args[2], args[3]
		if op == "del" || op == "rm" {
			deleteForm(client, *admin+"/api/networks/"+net+"/routes?cidr="+url.QueryEscape(cidr))
		} else if op == "add" && len(args) > 4 {
			postForm(client, *admin+"/api/networks/"+net+"/routes", url.Values{"cidr": {cidr}, "via": {args[4]}})
		} else {
			usage()
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: deviceportctl [options] <command>

node API:
  status                              show local node status
  connect <id>                        introduce a peer by node ID
  send <ip> <message>                 inject an ICMP echo packet

server admin API (-admin URL):
  networks                            list all networks, members and routes
  members [network]                   show one network
  auth <id> <on|off>                  authorize/revoke a member (default network)
  auth <network> <id> <on|off>        authorize/revoke in a named network
  require-auth <network> <on|off>     toggle member authorization for a network
  route add <network> <cidr> <via>    add a managed site-to-site route
  route del <network> <cidr>          remove a managed route`)
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func getJSON(c *http.Client, u string) {
	resp, err := c.Get(u)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		fatal(err)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func postForm(c *http.Client, u string, v url.Values) {
	resp, err := c.Post(u, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	printResponse(resp)
}

func deleteForm(c *http.Client, u string) {
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	printResponse(resp)
}

func printResponse(resp *http.Response) {
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal(err)
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}

// buildEcho creates a minimal IPv4 + ICMP echo request as hex for the demo API.
func buildEcho(dst, message string) (string, error) {
	ip := netParse4(dst)
	if ip == nil {
		return "", fmt.Errorf("invalid IPv4: %s", dst)
	}
	data := []byte(message)
	icmp := make([]byte, 8+len(data))
	icmp[0] = 8
	icmp[4], icmp[5] = 0x12, 0x34
	copy(icmp[8:], data)
	put16(icmp[2:4], checksum(icmp))

	pkt := make([]byte, 20+len(icmp))
	pkt[0] = 0x45
	put16(pkt[2:4], uint16(len(pkt)))
	pkt[8], pkt[9] = 64, 1
	copy(pkt[12:16], []byte{10, 66, 0, 1})
	copy(pkt[16:20], ip)
	copy(pkt[20:], icmp)
	put16(pkt[10:12], checksum(pkt[:20]))

	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(pkt)*2)
	for i, b := range pkt {
		out[i*2], out[i*2+1] = hexdigits[b>>4], hexdigits[b&0xf]
	}
	return string(out), nil
}

func netParse4(s string) []byte {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return nil
	}
	out := make([]byte, 4)
	for i, p := range parts {
		var v int
		if _, err := fmt.Sscanf(p, "%d", &v); err != nil || v < 0 || v > 255 {
			return nil
		}
		out[i] = byte(v)
	}
	return out
}

func put16(b []byte, v uint16) { b[0], b[1] = byte(v>>8), byte(v) }

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 != 0 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
