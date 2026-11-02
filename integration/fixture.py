"""Private HTTPS origin and loopback-only coordination service for the packaged suite."""
import json
import ssl
import threading
from collections import Counter
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

events = Counter()
released = set()
changed = threading.Condition()
forbidden = "https://forbidden.test"


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
        path = urlsplit(self.path).path
        if self.server.server_port == 9000:
            action, _, token = path.strip("/").partition("/")
            with changed:
                if action == "wait":
                    observed = changed.wait_for(lambda: events[token] > 0, 12)
                    self.reply("observed" if observed else "timeout", 200 if observed else 408)
                elif action == "release":
                    released.add(token)
                    changed.notify_all()
                    self.reply("released")
                elif action == "count":
                    self.reply(str(events[token]))
                else:
                    self.reply("", 404)
            return
        if self.headers.get("Host", "").split(":")[0] == "forbidden.test":
            with changed:
                events["forbidden"] += 1
                changed.notify_all()
            self.reply("canary")
            return
        if path.startswith("/hold/"):
            token = path.rsplit("/", 1)[1]
            with changed:
                events[token] += 1
                changed.notify_all()
                changed.wait_for(lambda: token in released, 35)
            self.reply("<p>released</p>")
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
        elif path == "/subresource":
            self.reply('<p id="state"></p><script>fetch("'+forbidden+
                       '/subresource-target",{mode:"no-cors"}).then(()=>finish("reached"),()=>finish("blocked"));'
                       'function finish(result){document.querySelector("#state").textContent=result}</script>')
        else:
            self.reply("<p>fixture</p>")


if __name__ == "__main__":
    control = ThreadingHTTPServer(("127.0.0.1", 9000), Handler)
    threading.Thread(target=control.serve_forever, daemon=True).start()
    server = ThreadingHTTPServer(("0.0.0.0", 443), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain("/tls/cert.pem", "/tls/key.pem")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
