# Deploying Hermes Chat

This runbook puts Hermes Chat on one Debian 13 host, serving HTTPS on port 443 with a Let's Encrypt certificate that the server obtains and renews itself. Names and addresses here are placeholders: `chat.example.com` is your DNS name and `203.0.113.10` is the host's address. Commands marked "on the host" run as root.

The files are in `deploy/`: `hermes-chat.service`, `hermes-chat.env.example`, `hermes-chat-upgrade`, `hermes-chat-admin`.

## 1. What you need

- A Debian 13 host that you can reach with `ssh` as root (port 22) and that the internet can reach on port 443. Port 80 is not used.
- A DNS name with an A record pointing at the host, and optionally an AAAA record.
- This repository, with Go and `just`, on the machine you build on (the build machine).

## 2. Before the first start, check DNS and IPv6

Do this before step 5. The first start asks Let's Encrypt for a certificate, and Let's Encrypt allows five failed validations per name per hour. Starting before DNS resolves spends them.

1. On the build machine, check the A record.

   ```
   dig +short A chat.example.com
   ```

   Worked: it prints `203.0.113.10`, the host's address.

2. Check the AAAA record.

   ```
   dig +short AAAA chat.example.com
   ```

   Worked: it prints the host's IPv6 address, or nothing because you published none. If it prints an address, do step 3. Let's Encrypt validates over IPv6 when the record exists, and a record that does not reach the host makes issuance fail even though IPv4 works.

3. Only if an AAAA record exists: prove it reaches the host.

   ```
   ssh -6 root@chat.example.com
   ```

   Worked: you get a shell on the host. Type `exit`. If it hangs or is refused, fix or remove the AAAA record and wait for DNS to catch up before going on.

## 3. Prepare the host

Once, on the host, in this order. Allowing 22 before enabling ufw keeps your ssh session alive.

1. Install the tools the scripts use and the firewall.

   ```
   apt-get install -y curl ufw
   ```

   Worked: it ends without an error.

2. Open ssh and HTTPS, then turn the firewall on.

   ```
   ufw allow 22/tcp
   ufw allow 443/tcp
   ufw --force enable
   ```

   Worked: the first two print `Rules updated` (and `Rules updated (v6)`); the last prints `Firewall is active and enabled on system startup`.

3. Create the service user. This does not create its home directory, `/var/lib/hermes-chat`; systemd creates it, owned by this user, the first time the service starts (step 5).

   ```
   useradd --system --home-dir /var/lib/hermes-chat --shell /usr/sbin/nologin hermes-chat
   ```

   Worked: no output. `getent passwd hermes-chat` prints one line.

4. Create the directory for the environment file.

   ```
   install -d -m 0755 /etc/hermes-chat
   ```

   Worked: no output.

## 4. Copy the deploy files

1. On the build machine, from the repository root:

   ```
   scp deploy/hermes-chat.service deploy/hermes-chat.env.example deploy/hermes-chat-upgrade deploy/hermes-chat-admin root@203.0.113.10:/root/
   ```

   Worked: four files listed at 100%.

2. On the host, install them.

   ```
   install -m 0644 /root/hermes-chat.service /etc/systemd/system/hermes-chat.service
   install -m 0755 /root/hermes-chat-upgrade /usr/local/sbin/hermes-chat-upgrade
   install -m 0755 /root/hermes-chat-admin /usr/local/sbin/hermes-chat-admin
   install -m 0644 /root/hermes-chat.env.example /etc/hermes-chat/env
   ```

   Worked: no output.

3. Set your domain in the environment file. Use an editor, or:

   ```
   sed -i 's/^HERMES_CHAT_TLS_DOMAIN=.*/HERMES_CHAT_TLS_DOMAIN=chat.example.com/' /etc/hermes-chat/env
   grep -v '^#' /etc/hermes-chat/env
   ```

   Worked: the grep prints the four settings, with `HERMES_CHAT_TLS_DOMAIN=` followed by your name. The name must be lower case with no quotes, scheme, or port. Values in this file are never quoted, because `hermes-chat-upgrade` and `hermes-chat-admin` read it without a shell and would keep the quotes.

4. Load the unit and have it start at boot.

   ```
   systemctl daemon-reload
   systemctl enable hermes-chat
   ```

   Worked: `enable` prints `Created symlink ... hermes-chat.service`. It does not start the service; there is no binary yet.

## 5. Build and install the first binary

1. On the build machine, build for the host. Use `arm64` in place of `amd64` if `uname -m` on the host prints `aarch64`.

   ```
   just build linux amd64
   ```

   Worked: `bin/hermes-chat-linux-amd64` exists.

2. Copy it to the host.

   ```
   scp bin/hermes-chat-linux-amd64 root@203.0.113.10:/root/
   ```

   Worked: the file is listed at 100%.

3. On the host, run the upgrade script on it.

   ```
   chmod +x /root/hermes-chat-linux-amd64
   hermes-chat-upgrade /root/hermes-chat-linux-amd64
   ```

   Worked: the last line is `installed`. This step creates `/var/lib/hermes-chat`, applies the database migrations, and starts the service. The script's health check is the first TLS handshake, which is what makes Let's Encrypt issue the certificate, so allow up to a minute.

   If it prints `new version failed its health check; nothing to roll back to`, the service is left stopped. Read the log (section 10), fix the cause (usually DNS, IPv6, or port 443 not reachable), and run the same command again. `candidate does not run on this host` means the wrong architecture or a damaged copy; nothing was stopped.

## 6. Check it

1. On the build machine:

   ```
   curl -s -o /dev/null -w '%{http_version} %{http_code}\n' --http2 https://chat.example.com/healthz
   ```

   Worked: it prints `2 200` (HTTP/2, status 200) with no certificate warning.

2. On the host:

   ```
   systemctl status hermes-chat
   ```

   Worked: `Active: active (running)`.

3. On the host:

   ```
   ufw status
   ```

   Worked: `22/tcp` and `443/tcp` are allowed, each listed for IPv4 and for `(v6)`.

4. On the host:

   ```
   ls -ld /var/lib/hermes-chat
   ```

   Worked: `drwx------ ... hermes-chat hermes-chat ...`.

## 7. Create people and agents

Do this after step 5: the state directory and the database do not exist until the service has started once. `hermes-chat-admin` runs the `hermes-chat` command as the `hermes-chat` user with the settings from `/etc/hermes-chat/env`, so every file it touches is owned by the service. Run it as root, in a terminal, so the password prompt works.

1. Create a person. It asks for the password twice.

   ```
   hermes-chat-admin user add kai
   ```

   Worked: `created user kai (<id>)`.

2. Create an agent.

   ```
   hermes-chat-admin agent add helper "Helper"
   ```

   Worked: `created agent @helper (<id>)` and a `token:` line. The token is shown once; copy it now. `hermes-chat-admin agent rotate helper` issues a new one.

The other commands in the README's Commands section work the same way (`user list`, `user passwd`, `agent revoke`, ...). `hermes-chat-admin serve` is refused; use `systemctl`.

## 8. Upgrade

1. On the build machine, build and copy.

   ```
   just build linux amd64
   scp bin/hermes-chat-linux-amd64 root@203.0.113.10:/root/
   ```

2. On the host:

   ```
   hermes-chat-upgrade /root/hermes-chat-linux-amd64
   ```

   Worked: the last line is `upgraded; previous binary kept at /usr/local/bin/hermes-chat.previous`.

What the script does, in order:

1. Runs the candidate with no arguments and refuses (exit 1, nothing stopped) unless it prints hermes-chat's usage. This catches the wrong architecture and a truncated copy.
2. Reads `/etc/hermes-chat/env`, and refuses if the database path is not absolute or, with `autocert`, the domain is missing.
3. Stops the service. Open event streams drop here; clients reconnect when it is back.
4. Copies the database (and its `-wal` and `-shm` files) to `/var/lib/hermes-chat/backups/<UTC timestamp>/`.
5. Moves the running binary to `hermes-chat.previous` and installs the candidate.
6. Starts the service and polls `/healthz` for up to 60 seconds.
7. If healthy, prints the line above and deletes all but the newest five backup directories.

If the health check fails, the script stops the service, puts back the previous binary and the database as it was before the upgrade (so any migration the new version ran is undone), starts the old version, and exits 1 with `new version failed its health check; rolled back to the previous version`. If the session drops or the script is interrupted after the service was stopped, it does the same and prints `upgrade failed; ...`; when the binary had not been swapped yet the message is `upgrade failed; restarted the previous version`.

## 9. Roll back by hand

Use this when an upgrade passed its health check but the new version is wrong. It discards everything written since the database copy, which is the time of that upgrade. On the host:

1. Pick the copy to restore. Names are UTC times, newest last.

   ```
   ls /var/lib/hermes-chat/backups
   ```

   Worked: lines like `20261012T093000Z`. Use the one taken by the upgrade you are undoing.

2. Stop the service and put the previous binary back.

   ```
   systemctl stop hermes-chat
   mv -f /usr/local/bin/hermes-chat.previous /usr/local/bin/hermes-chat
   ```

   Worked: no output.

3. Replace the database with the copy, removing stale journal files first. Substitute the stamp.

   ```
   rm -f /var/lib/hermes-chat/chat.db /var/lib/hermes-chat/chat.db-wal /var/lib/hermes-chat/chat.db-shm
   cp -p /var/lib/hermes-chat/backups/20261012T093000Z/chat.db* /var/lib/hermes-chat/
   chown hermes-chat:hermes-chat /var/lib/hermes-chat/chat.db*
   ```

   Worked: no output; `ls -l /var/lib/hermes-chat` shows `chat.db` owned by `hermes-chat`.

4. Start and check.

   ```
   systemctl start hermes-chat
   curl -s -o /dev/null -w '%{http_code}\n' --resolve chat.example.com:443:127.0.0.1 https://chat.example.com/healthz
   ```

   Worked: `200`.

## 10. Logs

```
journalctl -u hermes-chat -o cat -f
```

Each request is one JSON line. Certificate trouble shows as lines containing `acme/autocert`. A public host also logs `TLS handshake error` lines from scanners that ask for other names; they are expected and harmless.

## 11. What lives where

| Path | What |
|---|---|
| `/usr/local/bin/hermes-chat` | the running binary |
| `/usr/local/bin/hermes-chat.previous` | the binary before the last upgrade |
| `/etc/hermes-chat/env` | settings (unquoted `NAME=value` lines) |
| `/etc/systemd/system/hermes-chat.service` | the unit |
| `/usr/local/sbin/hermes-chat-upgrade` | installs a new binary |
| `/usr/local/sbin/hermes-chat-admin` | admin commands as the service user |
| `/var/lib/hermes-chat/chat.db` | the database (plus `-wal` and `-shm` while running) |
| `/var/lib/hermes-chat/attachments/` | uploaded files |
| `/var/lib/hermes-chat/certs/` | issued certificates and the Let's Encrypt account key |
| `/var/lib/hermes-chat/backups/` | the last five pre-upgrade database copies |

Back up the database, `attachments/`, and, to avoid asking Let's Encrypt for a new certificate after a rebuild, `certs/`. Stop the service first, or copy the `-wal` and `-shm` files with the database. `backups/` is not a backup: it holds only the last five pre-upgrade copies and sits on the same disk. Scheduled off-host backups are not part of this setup.

## 12. Running behind a reverse proxy instead

If something else terminates TLS, set these in `/etc/hermes-chat/env` instead of the autocert lines, and open only the proxy's port:

```
HERMES_CHAT_TLS_MODE=off
HERMES_CHAT_HTTP_ADDR=127.0.0.1:8080
HERMES_CHAT_TRUSTED_PROXIES=127.0.0.1
```

The proxy must pass the original `Host` header through (nginx: `proxy_set_header Host $host;`), or every login returns 403; see Configuration in the README. `hermes-chat-upgrade` then checks `http://127.0.0.1:<port>/healthz`. The unit's `CAP_NET_BIND_SERVICE` is harmless for a port above 1023.
