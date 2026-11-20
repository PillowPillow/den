# SSH in the VM

**Warning: every VM can ask your SSH agent to sign with every key it holds.** This includes a
stopped VM once it starts again. Load only the keys your VMs may use, and give each nest only the
network access it needs.

On this page, the **SSH agent** is the `ssh-agent` program on your host that holds your keys in
memory. A **nest** is what you declare in a `den.yaml`; an **instance** is the VM den creates from
it (see the [README](../README.md#words-den-uses)).

- [How SSH works in the VM](#how-ssh-works-in-the-vm)
- [Check](#check)
- [Load a key](#load-a-key)
- [What a loaded key gives a VM](#what-a-loaded-key-gives-a-vm)
- [Limit what a VM can do with a key](#limit-what-a-vm-can-do-with-a-key)
- [Turn SSH off for every VM](#turn-ssh-off-for-every-vm)
- [Many keys](#many-keys)
- [What the VM does not get](#what-the-vm-does-not-get)
- [Troubleshooting](#troubleshooting)

## How SSH works in the VM

**The VM has no SSH keys. It borrows your SSH agent.** Your `~/.ssh` stays on your host: in the VM,
`~` is `/home/agent` and holds no key, no `~/.ssh/config` and no `known_hosts`. sbx connects the VM
to the SSH agent of your host (`SSH_AUTH_SOCK`) through a socket in the VM. When `ssh` or `git` runs
in the VM, the agent on your host signs. The VM never sees a private key, so nothing in the VM can
copy one.

```
Host                                          VM
~/.ssh/id_ed25519 (file)                      /home/agent/.ssh   (no key)
      | ssh-add
      v
ssh-agent (key in memory)  <----------------  agent socket  <--  ssh, git
```

**A key file is not a loaded key.** On your host, `ssh` reads `~/.ssh` directly, so it works with an
empty agent. In the VM, the agent is the only source of keys. An empty agent gives
`Permission denied (publickey)` in the VM even when `ssh` works on your host. The reverse also
happens: with `AddKeysToAgent yes` in your `~/.ssh/config`, every key `ssh` uses on your host is added
to the agent, and so reaches the VMs, without any `ssh-add`.

**All VMs share one agent.** The sbx daemon forwards one agent to every VM. An `sbx` or `den`
command run with another `SSH_AUTH_SOCK` can switch that agent for every VM, and the switch outlives
the command (measured with sbx v0.53.0). den therefore cannot give a nest its own keys.

## Check

```bash
ssh-add -l
sbx exec <instance> ssh-add -l
```

- `ssh-add -l`, on your host, lists the loaded keys.
- `sbx exec <instance> ssh-add -l`, in the VM, must list the same keys.

`The agent has no identities.` means the agent is empty. A key you add appears in a running VM at
once; you do not need to remove and create the instance again.

When `den up` creates a VM, it checks the agent that sbx forwards: the socket of
`ssh.agentSocketPath`, or else the `SSH_AUTH_SOCK` of the shell you run `den` from. It prints
`note: the SSH agent at <socket> holds no key`, `note: no SSH agent answers at <socket>` or
`note: SSH_AUTH_SOCK is not set` when the VM may not be able to use it. "May": sbx can keep
serving the agent of an earlier `sbx` or `den` call (see [How SSH works in the VM](#how-ssh-works-in-the-vm)).
It is a note, not an error: an empty agent is the safe state, and a nest that does not use SSH
needs no key. With `ssh.agentForwardingEnabled false`, den checks nothing.

## Load a key

Load only the keys your VMs may use, for example the key of the git server your nests push to:

```bash
ssh-add ~/.ssh/<key>
ssh-add -t 8h ~/.ssh/<key>
```

- `ssh-add ~/.ssh/<key>` loads the key until you log out or remove it.
- `ssh-add -t 8h ~/.ssh/<key>` loads the key for 8 hours.

To load it in every session, put the `ssh-add` line in `~/.zshrc`. Adding a key that is already
loaded is harmless.

**A key with a passphrase, on macOS:** run `ssh-add --apple-use-keychain ~/.ssh/<key>` once; it saves the
passphrase in the macOS Keychain. Then `ssh-add --apple-load-keychain` in `~/.zshrc` reloads the
keys saved that way without asking. A key without a passphrase has nothing in the Keychain: load it
with `ssh-add ~/.ssh/<key>`.

**Remove keys:**

```bash
ssh-add -d ~/.ssh/<key>
ssh-add -D
```

- `ssh-add -d ~/.ssh/<key>` removes one key.
- `ssh-add -D` removes every key.

### Option: load every key

This line loads every private key in `~/.ssh`. It finds them by content, so it does not skip a key
that has no `.pub` file:

```bash
ssh-add $(grep -l 'PRIVATE KEY' ~/.ssh/* 2>/dev/null)
```

**Risk:** every VM can then use every key you own, as described in the next section.

## What a loaded key gives a VM

sbx cannot hide a key from one VM. What a VM can do with a key depends on the network:

- **Log in to a server** only if the VM's network policy lets it reach that server. sbx enforces the
  policy on every connection the VM opens.
- **SSH over port 443 often bypasses your nest's rules.** sbx's `balanced` policy allows hosts such
  as `ssh.github.com:443` and `altssh.gitlab.com:443` for every VM. With `balanced`, every VM can
  reach your GitHub and GitLab accounts over SSH with every loaded key, even if no nest declares
  them. Check with `sbx policy check network ssh.github.com:443 --sandbox <instance>`.
- **Forward the agent onward.** A VM that can log in to an allowed host can run `ssh -A` to it, and
  use your agent from that host, where sbx's policy no longer applies.
- **Sign without a network.** The agent signs any data. A VM can sign git commits or tags with your
  key (`gpg.format=ssh`) in a mounted repo; they look like yours when you push them.

For the network part to protect anything, each VM must get **only the network access of its own
nest**:

- Declare SSH hosts in the nest (or its template), with the port:

  ```yaml
  nests:
    api:
      network:
        allow: ["git.example.com:22", "*.dev.example.com:22"]
  ```

  `*.` matches one label: `*.dev.example.com` covers `web.dev.example.com`, not
  `a.web.dev.example.com`. A host without a port allows every port.
- `network.allow` in the global `den.yaml` reaches **every** nest. Keep only what every nest may use
  there.
- The global sbx policy (`local-policy`) applies to every VM. A rule added there by hand
  (`sbx policy allow network <host>`) opens that host to every VM. See
  [security.md](security.md#what-the-vm-can-reach).

Check what a VM can reach:

```bash
sbx policy check network git.example.com:22 --sandbox <instance>
```

- `sbx policy check network git.example.com:22 --sandbox <instance>` answers `Allowed` or `Denied`.

To block a host for one instance: `sbx policy deny network <host> --sandbox <instance>`.

### Accepted residual risks

- A VM can use every loaded key toward every host it reaches, including hosts the global policy
  allows on port 443. Two nests that both reach GitHub both reach your GitHub account with every key
  loaded.
- A VM can forward your agent to a host it can log in to, and sign there.
- A VM can sign commits and tags with any loaded key.

## Limit what a VM can do with a key

These options act on the key in the SSH agent, so they apply to every VM and to your own `ssh` on the
host. None of them is set up by den.

| Option | Effect | Cost |
|---|---|---|
| Load fewer keys, `ssh-add -t <time>` | a key not loaded cannot be used | load the key before you need it |
| `ssh-add -h <host> ~/.ssh/<key>` | the agent signs a login with that key only toward `<host>` (destination constraint, see `man ssh-add`) | `<host>`'s host key must be in your host's `~/.ssh/known_hosts`; other uses of the key are refused |
| `ssh-add -c ~/.ssh/<key>` | the agent asks you to confirm each signature on your host | needs an `ssh-askpass` program, which macOS does not ship (unverified: without it, every signature may be refused); a prompt for every `git fetch` of every VM |
| `ssh.agentForwardingEnabled false` | no VM gets the agent | no SSH in any VM, see below |

`sbx settings ls` also shows `ssh.agentSocketPath`, a fixed host agent socket for every VM. Running a
second `ssh-agent` that holds only the VM keys, and setting this path to its socket, would keep your
other keys out of every VM. Unverified: den has not tested it.

## Turn SSH off for every VM

If no nest needs SSH, turn agent forwarding off. The setting applies to every sbx VM:

```bash
sbx settings set ssh.agentForwardingEnabled false
sbx daemon restart
```

The setting takes effect after the sbx daemon restarts (`sbx settings ls` marks it `RESTART yes`).
Restarting the daemon may stop running VMs; `den up` starts them again. Turn forwarding back on with
`true`.

## Many keys

`ssh` in the VM offers the agent's keys one by one, and a server closes the connection after a few
failures (`Too many authentication failures`; the OpenSSH server allows 6 by default,
`MaxAuthTries`). With many keys loaded, name the key in the VM: copy its **public** key
(`<key>.pub`, never the private one) into the VM and run `ssh -o IdentitiesOnly=yes -i <key>.pub ...`.
The agent still signs.

## What the VM does not get

Your `~/.ssh/config` and `~/.ssh/known_hosts` stay on your host.

- `Host` aliases, `User`, `Port` and `ProxyJump` do not apply in the VM. Give them on the command line
  (`ssh -J jump user@host`) or in the tool's own config.
- The VM does not know your servers' host keys. The first connection to a host asks to confirm its
  key; a non-interactive `git` fails with `Host key verification failed.` Add the key in the VM once:
  `ssh-keyscan git.example.com >> ~/.ssh/known_hosts`, after you check its fingerprint. To give every
  VM your host keys, ship `files/home/.ssh/known_hosts` in a profile kit
  ([den-yaml.md](den-yaml.md#profiles)).
- Do not copy `UseKeychain` into a config file in the VM: the VM's `ssh` is the Linux one, and it
  stops with `Bad configuration option: usekeychain`.

## Troubleshooting

| Symptom in the VM | Cause | Fix |
|---|---|---|
| `The agent has no identities.` | no key loaded on your host | `ssh-add ~/.ssh/<key>` on your host |
| `Permission denied (publickey)`, but `ssh` works on your host | the key your host uses is a file, not loaded in the agent; often a key with no `.pub`, or one named only in `~/.ssh/config` | `ssh-add ~/.ssh/<that key>`, then `sbx exec <instance> ssh-add -l` |
| `Permission denied (publickey)`, key loaded | wrong user (git servers want `git@`), or the server does not accept that key | use `git@<host>`; check the key is registered on the server |
| `Host key verification failed.` | the VM has no `known_hosts` entry for the host | see [What the VM does not get](#what-the-vm-does-not-get) |
| `Too many authentication failures` | many keys loaded, the server stops before the right one | [Many keys](#many-keys) |
| the connection fails or times out | the nest's network policy does not allow the host and port | `sbx policy check network <host>:22 --sandbox <instance>`, then add `<host>:22` to the nest |
| `Could not resolve hostname <alias>` | a `Host` alias from your `~/.ssh/config` | use the real host name |
| `Could not open a connection to your authentication agent` | agent forwarding is off, or no agent runs on your host | `sbx settings ls`, and `ssh-add -l` on your host |
