#!/usr/bin/env python3
# Reads Claude's REAL /usage (the slash command), NOT the HTTP endpoint. Drives the TUI in a pty,
# runs /usage, parses "Current session" + "Current week" % and reset times → JSON with reset MINUTES.
import os, pty, time, sys, select, re, json
from datetime import datetime, timedelta
try:
    from zoneinfo import ZoneInfo
    LON = ZoneInfo("Europe/London")
except Exception:
    LON = None

def reset_minutes(s):
    # "3:20pm (Europe/London)"  or  "Jun 18 at 10pm (Europe/London)"
    if not s or LON is None: return -1
    now = datetime.now(LON)
    s = s.strip()
    mday = re.search(r'([A-Z][a-z]{2})\s*(\d{1,2}).*?(\d{1,2})(?::(\d{2}))?\s*([ap]m)', s)
    mtime = re.search(r'(\d{1,2})(?::(\d{2}))?\s*([ap]m)', s)
    months = {m:i+1 for i,m in enumerate(["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"])}
    try:
        if mday:
            mon=months[mday.group(1)]; day=int(mday.group(2)); hr=int(mday.group(3)); mi=int(mday.group(4) or 0); ap=mday.group(5)
            if ap=='pm' and hr!=12: hr+=12
            if ap=='am' and hr==12: hr=0
            yr=now.year
            t=datetime(yr,mon,day,hr,mi,tzinfo=LON)
            if t < now - timedelta(days=1): t=datetime(yr+1,mon,day,hr,mi,tzinfo=LON)
        elif mtime:
            hr=int(mtime.group(1)); mi=int(mtime.group(2) or 0); ap=mtime.group(3)
            if ap=='pm' and hr!=12: hr+=12
            if ap=='am' and hr==12: hr=0
            t=now.replace(hour=hr,minute=mi,second=0,microsecond=0)
            if t <= now: t=t+timedelta(days=1)
        else:
            return -1
        return max(0,int((t-now).total_seconds()//60))
    except Exception:
        return -1

def seed_onboarding(configdir):
    # A fresh CLAUDE_CONFIG_DIR profile launches the interactive FIRST-RUN flow (theme + login
    # selection) which blocks /usage, even with valid creds. Mark onboarding complete so the TUI
    # goes straight to the prompt → /usage works. Only touches UI-state flags, never auth.
    if not configdir: return
    import json
    p = os.path.join(os.path.expanduser(configdir), ".claude.json")
    try:
        d = json.load(open(p))
    except Exception:
        return
    if d.get("hasCompletedOnboarding") is True:
        return
    d["hasCompletedOnboarding"] = True
    d.setdefault("theme", "dark")
    d["numStartups"] = max(5, int(d.get("numStartups") or 0))
    d.setdefault("lastOnboardingVersion", "2.1.165")
    d["bypassPermissionsModeAccepted"] = True
    try:
        json.dump(d, open(p, "w"), indent=2)
    except Exception:
        pass

def probe(configdir):
    seed_onboarding(configdir)  # self-heal: skip first-run onboarding so /usage is reachable
    env = dict(os.environ)
    if configdir: env["CLAUDE_CONFIG_DIR"]=os.path.expanduser(configdir)
    env["TERM"]="xterm-256color"; env["COLUMNS"]="100"; env["LINES"]="44"
    # Run from a neutral temp dir with NO project MCP/CLAUDE.md so the TUI starts light + fast
    # (/usage is account-global, so cwd/project context doesn't affect the numbers).
    neutral = os.path.join(os.path.expanduser("~"), ".cw-accounts", "_usage_probe_cwd")
    try: os.makedirs(neutral, exist_ok=True)
    except Exception: neutral = "/tmp"
    pid, fd = pty.fork()
    if pid==0:
        try: os.chdir(neutral)
        except Exception: pass
        os.execvpe("claude",["claude","--strict-mcp-config"],env); os._exit(1)
    buf=b""
    def drain(t):
        nonlocal buf; end=time.time()+t
        while time.time()<end:
            r,_,_=select.select([fd],[],[],0.3)
            if r:
                try: buf+=os.read(fd,65536)
                except OSError: break
    drain(7); os.write(fd,b"/usage\r"); drain(8)
    try: os.write(fd,b"\x03\x03")
    except OSError: pass
    drain(0.5)
    try: os.close(fd)
    except OSError: pass
    txt=buf.decode("utf-8","replace")
    for pat in [r'\x1b\[[0-9;?]*[A-Za-z]', r'\x1b\][^\x07]*\x07', r'\x1b[=>]', r'[\x00-\x08\x0b\x0c\x0e-\x1f]']:
        txt=re.sub(pat,'',txt)
    raw=[l.strip() for l in txt.splitlines()]
    blob=" ".join(raw).lower()
    notready=("select login method" in blob) or ("choose the text style" in blob and "current session" not in blob)
    def pct_after(anchor):
        for i,l in enumerate(raw):
            if anchor in l.lower().replace(" ",""):
                for j in range(i,min(i+5,len(raw))):
                    m=re.search(r'(\d+)\s*%\s*used', raw[j].replace(" ",""))
                    if m:
                        rst=""
                        for k in range(j,min(j+3,len(raw))):
                            rm=re.search(r'[Rr]esets?\s*(.+)', raw[k])
                            if rm: rst=rm.group(1).strip()[:40]; break
                        return int(m.group(1)), rst
        return -1,""
    s,sr=pct_after("currentsession"); w,wr=pct_after("currentweek")
    return {"ok": (s>=0 or w>=0), "session": s, "week": w,
            "sessionResetMin": reset_minutes(sr), "weekResetMin": reset_minutes(wr),
            "sessionReset": sr, "weekReset": wr, "notReady": notready}

def probe_reliable(cd, tries=2):
    last={"ok":False,"session":-1,"week":-1,"notReady":False}
    for _ in range(tries):
        try:
            r=probe(cd)
        except Exception as e:
            last={"ok":False,"error":str(e),"session":-1,"week":-1,"notReady":False}; continue
        if r.get("ok"): return r           # got real numbers
        last=r
        if r.get("notReady"): return r     # genuine onboarding/login state — retrying won't help
    return last

if __name__=="__main__":
    cd=sys.argv[1] if len(sys.argv)>1 else ""
    print(json.dumps(probe_reliable(cd)))
