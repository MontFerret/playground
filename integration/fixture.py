"""Private HTTPS origin and loopback-only coordination service for the packaged suite."""
import json
import ipaddress
import os
import socket
import socketserver
import ssl
import struct
import threading
from collections import Counter
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

events = Counter()
released = set()
changed = threading.Condition()
forbidden = "https://forbidden.test"
approved_ip = "185.199.108.153"
private_ip = "172.30.247.10"
private_ipv6 = "fd42:5047::10"
dns_mode = "public"


def count(token):
    with changed:
        events[token] += 1
        changed.notify_all()


def observe_peer(address):
    interval = threading.Event()
    while True:
        try:
            with socket.create_connection((address, 9223), timeout=0.1):
                count("exposed-cdp")
        except OSError:
            pass
        count("cdp-observations")
        interval.wait(0.1)


class DNS(socketserver.BaseRequestHandler):
    def handle(self):
        packet, server = self.request
        if len(packet) < 17 or packet[4:6] != b"\x00\x01":
            return
        pos = 12
        labels = []
        while pos < len(packet) and packet[pos]:
            length = packet[pos]
            if length > 63 or pos + length >= len(packet):
                return
            labels.append(packet[pos + 1:pos + length + 1])
            pos += length + 1
        if pos + 5 > len(packet):
            return
        question = packet[12:pos + 5]
        name = b".".join(labels).lower()
        addresses = []
        known = name in (b"mockery.ferretlang.org", b"forbidden.test", b"outside-proxy.test")
        if known and question[-4:] == b"\x00\x01\x00\x01":
            if name == b"mockery.ferretlang.org":
                with changed:
                    events["dns-queries"] += 1
                    addresses = {"public": [approved_ip], "private": [private_ip],
                                 "mixed": [approved_ip, private_ip],
                                 "rebind": [approved_ip if events["dns-queries"] == 1 else private_ip]}[dns_mode]
            else:
                addresses = [approved_ip if name == b"forbidden.test" else private_ip]
        header = packet[:2] + struct.pack("!HHHHH", 0x8180 if known else 0x8183, 1, len(addresses), 0, 0)
        records = b"".join(b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 0, 4) + socket.inet_aton(ip) for ip in addresses)
        server.sendto(header + question + records, self.client_address)


class CanaryServer(ThreadingHTTPServer):
    address_family = socket.AF_INET6

    def __init__(self, port, tls=None):
        self.tls = tls
        super().__init__(("::", port), Handler)

    def server_bind(self):
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()

    def get_request(self):
        conn, remote = super().get_request()
        address, port, *_ = conn.getsockname()
        ip = ipaddress.IPv6Address(address)
        count(f"tcp-{ip.ipv4_mapped or ip}-{port}")
        return conn, remote

    def process_request_thread(self, request, client_address):
        if self.tls is not None:
            request.settimeout(5)
            try:
                request = self.tls.wrap_socket(request, server_side=True)
            except OSError:
                request.close()
                return
            request.settimeout(None)
        super().process_request_thread(request, client_address)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, body="", status=200, kind="text/html"):
        data = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except OSError:
            pass

    def do_GET(self):
        global dns_mode
        path = urlsplit(self.path).path
        if self.server.server_port == 9000:
            action, _, token = path.strip("/").partition("/")
            with changed:
                if action == "wait":
                    observed = changed.wait_for(lambda: events[token] > 0, 45)
                    self.reply("observed" if observed else "timeout", 200 if observed else 408)
                elif action == "release":
                    released.add(token)
                    changed.notify_all()
                    self.reply("released")
                elif action == "count":
                    self.reply(str(events[token]))
                elif action == "after":
                    key, _, previous = token.partition("=")
                    observed = changed.wait_for(lambda: events[key] > int(previous), 10)
                    self.reply(str(events[key]), 200 if observed else 408)
                elif action == "dns" and token in ("public", "private", "mixed", "rebind"):
                    dns_mode = token
                    events["dns-queries"] = 0
                    self.reply("configured")
                else:
                    self.reply("", 404)
            return
        if path.startswith("/canary/"):
            count(path.removeprefix("/canary/"))
            self.reply("canary", 403 if self.headers.get("Upgrade", "").lower() == "websocket" else 200)
            return
        if self.headers.get("Host", "").split(":")[0] == "forbidden.test":
            with changed:
                events["forbidden"] += 1
                changed.notify_all()
            self.reply("canary")
            return
        if path == "/socket" and self.headers.get("Upgrade", "").lower() == "websocket":
            with changed:
                events["allowed-socket"] += 1
            self.reply("handshake observed", 403)
            return
        if path.startswith("/hold/"):
            token = path.rsplit("/", 1)[1]
            with changed:
                events[token] += 1
                changed.notify_all()
                changed.wait_for(lambda: token in released, 70)
            self.reply("<p>released</p>")
        elif path == "/fast":
            with changed:
                events["fast"] += 1
            self.reply('<p id="state">ready</p>')
        elif path == "/scenarios/dynamic-products/basic/":
            self.reply("""<main></main><script>
              fetch('/cards.json').then(r=>r.json()).then(cards=>{for(const c of cards){
                const card=document.createElement('article'); card.className='product-card';
                for(const [key,value] of Object.entries(c)){
                  const el=document.createElement('span');el.className='product-'+key;
                  el.textContent=value;card.append(el)
                }
                document.querySelector('main').append(card)
              }});
            </script>""")
        elif path == "/cards.json":
            self.reply(json.dumps([dict(brand="Northwind", title="Laptop", price="$1299.50"),
                                   dict(brand="Contoso", title="Mouse", price="$19.99")]), kind="application/json")
        elif path in ("/write", "/read"):
            script = ""
            if path == "/write":
                script = """document.cookie='run=private; Secure';localStorage.run='private';
                    sessionStorage.run='private';window.open('/popup','owned-popup');"""
            self.reply('<p id="state"></p><script>'+script+
                       """document.querySelector('#state').textContent=JSON.stringify(
                       [document.cookie,localStorage.run||'',sessionStorage.run||'']);</script>""")
        elif path == "/redirect":
            self.send_response(302)
            self.send_header("Location", forbidden + "/redirect-target")
            self.end_headers()
        elif path.startswith("/session/"):
            token = json.dumps(path.rsplit("/", 1)[1])
            self.reply('<p id="state"></p><script>const token='+token+""";
                document.cookie='run='+token+'; Secure; Path=/';localStorage.run=token;
                const state=document.querySelector('#state');
                // FQL signals from the loaded page before waiting for its final value.
                const observer=new MutationObserver(()=>{
                  observer.disconnect();
                  fetch('/hold/'+token).then(()=>{
                    state.textContent=JSON.stringify([document.cookie,localStorage.run]);
                  });
                });
                observer.observe(state,{childList:true});
                </script>""")
        elif path.startswith("/hold-page/"):
            route = json.dumps("/canary/" + path.rsplit("/", 1)[1])
            self.reply('''<p id="state"></p><script>
                const state = document.querySelector("#state");
                new MutationObserver((_, observer) => {
                  observer.disconnect();
                  fetch(''' + route + ''');
                }).observe(state, {childList: true});
                </script>''')
        elif path == "/sockets":
            self.reply("""<p id="state"></p><script>
                function handshake(host) {
                  return new Promise(resolve => {
                    const ws=new WebSocket('wss://'+host+'/socket');
                    ws.onopen=()=>{ws.close();resolve('opened')};ws.onerror=()=>resolve('closed');
                  });
                }
                Promise.all([handshake(location.host),handshake('forbidden.test')]).then(outcomes=>{
                  document.querySelector('#state').textContent=JSON.stringify(outcomes);
                });</script>""")
        elif path == "/subresource":
            self.reply('<p id="state"></p><script>fetch("'+forbidden+
                       '/subresource-target",{mode:"no-cors"}).then(()=>finish("reached"),()=>finish("blocked"));'
                       'function finish(result){document.querySelector("#state").textContent=result}</script>')
        else:
            self.reply("<p>fixture</p>")


if __name__ == "__main__":
    control = ThreadingHTTPServer(("127.0.0.1", 9000), Handler)
    threading.Thread(target=control.serve_forever, daemon=True).start()
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain("/tls/cert.pem", "/tls/key.pem")
    for port in (80, 443, 8081, 8443):
        server = CanaryServer(port, tls if port in (443, 8443) else None)
        threading.Thread(target=server.serve_forever, daemon=True).start()
    with socketserver.UDPServer(("0.0.0.0", 53), DNS) as resolver:
        threading.Thread(target=observe_peer, args=(os.environ["PLAYGROUND_CDP_PEER"],), daemon=True).start()
        resolver.serve_forever()
