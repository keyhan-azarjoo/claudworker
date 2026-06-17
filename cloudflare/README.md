# Expose the control panel — securely, free, no password stored

The panel binds to **127.0.0.1:8787** (localhost only). To reach it from the internet we use a
**Cloudflare Tunnel** (no open ports, no port-forwarding) and put **Cloudflare Access** in front so only
you can open it (login = a one-time code emailed to **keyhanazarjoo@gmail.com**).

> We do **not** store your Cloudflare password. `cloudflared` authorises once via your browser and saves
> its own scoped cert under `~/.cloudflared/`. (Please rotate the password you pasted in chat.)

## Important: example.com DNS is on GoDaddy, not Cloudflare
So `panel.example.com` can only be Access-protected if the zone is moved to Cloudflare — that affects your
live site + email, so it's **your decision** (don't let an agent do it). Two paths:

### Option A — instant, zero DNS change (use today)
```bash
./cloudflare/setup-tunnel.sh quick
```
Starts a quick tunnel and prints a temporary `https://<random>.trycloudflare.com` URL to your local panel.
Good for trying it now. (No Access in front — keep the URL private; it rotates each run.)

### Option B — permanent panel.example.com (after moving the zone to Cloudflare)
1. Add `example.com` to Cloudflare and switch nameservers at GoDaddy (one-time, your call).
2. Then:
```bash
./cloudflare/setup-tunnel.sh named panel.example.com
```
This installs `cloudflared`, logs you in (browser), creates a named tunnel `claudworker-agents`, routes
`panel.example.com` → your local panel, and installs it as a background service.
3. In the Cloudflare **Zero Trust** dashboard → Access → Applications: add `panel.example.com`, policy =
allow email `keyhanazarjoo@gmail.com` (one-time PIN). Now only you can open it.

## Run order
```bash
cp config.example.json config.json
(cd master && go build -o ../claudworker .)
./claudworker -serve                 # panel on http://127.0.0.1:8787
./cloudflare/setup-tunnel.sh quick   # or: named panel.example.com
```
