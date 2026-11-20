# Templates

A **template** is what a nest's VM starts from. It is one of two things:

- an **official image**: a Docker sandbox image that sbx pulls. Nothing to build.
- a template **you build** with `den build`: an official image plus your kits, network access and
  provisioning scripts, saved by sbx as a local template.

Start with an official image. Build your own when every VM of a nest needs the same tools installed.
The words nest, instance and kit are defined in the [README](../README.md#words-den-uses).

- [Choosing a template for a nest](#choosing-a-template-for-a-nest)
- [Official images](#official-images)
- [Build your own template](#build-your-own-template)
- [Declaring a template](#declaring-a-template)
- [The definition file](#the-definition-file)
- [`den build`](#den-build)
- [After a rebuild](#after-a-rebuild)
- [The image must keep the VM alive](#the-image-must-keep-the-vm-alive)
- [The image must provide `jq`](#the-image-must-provide-jq)
- [Default Claude settings from a kit](#default-claude-settings-from-a-kit)

## Choosing a template for a nest

`template:` on a nest names its template. Without it, den uses `default_template:` of the nest's own
file, then `default_template:` of the global file. den code has no default: when no value exists
anywhere, `den up` refuses, names the key to set, and lists the official images it reads from
Docker Hub.

den reads a value in this order:

1. a key declared under `templates:` in a `den.yaml`: the project file first (it masks a global key
   of the same name), then the global file;
2. otherwise an **official image**.

## Official images

A short name without `/` is completed to `docker.io/docker/sandbox-templates:<name>`:

```yaml
default_template: claude-code-docker   # docker.io/docker/sandbox-templates:claude-code-docker
```

A value with a `/` is a full image address, used as written. A short name with a tag
(`ubuntu:22.04`) is refused: write the full address of any other image
(`docker.io/library/ubuntu:22.04`). An official image needs no
`den build`: sbx pulls it when the instance is created.

## Build your own template

The repository ships a working template in [`examples/den-template/`](../examples/den-template/):

```
examples/den-template/
  den-template.yaml            # the definition
  kits/example-kit/spec.yaml   # a local kit, added to every VM on this template
  steps/10-marker.sh           # a provisioning script, run once at build time
```

1. Clone this repository (`git clone https://github.com/PillowPillow/den.git`). Declare the
   template in your global file, `~/.den/den.yaml` (it starts with `schema: 1`). Replace
   `<den clone>` with the absolute path of your clone:

   ```yaml
   templates:
     example:
       dir: <den clone>/examples/den-template
   ```

2. Build it:

   ```bash
   den build example
   ```

   den starts a throwaway VM named `example-build` on `claude-code-docker`, runs
   `steps/10-marker.sh` in it (its output, `provisioned by den-template`, is printed), and saves
   the result as the sbx template `example:v2`.

3. Use it in a nest of the same file:

   ```yaml
   nests:
     demo:
       template: example
       repos:
         - path: ~/src/demo
   ```

4. Run `den up demo`. The VM starts from `example:v2`, with `example-kit` added.

To make your own, copy the folder, rename the template in `den-template.yaml`, and replace the step
with your install commands. Use the new name as the key under `templates:` in `den.yaml` (or set
`name:` to it). A template name is lowercase and must be a legal sbx sandbox name once `-build` is
appended. After you edit a step, run `den build <name>` again, then
[recreate the instances](#after-a-rebuild).

## Declaring a template

`templates.<key>` in a `den.yaml` points to a **location**, a folder that holds a
`den-template.yaml`:

| Key | Meaning |
|---|---|
| `dir` | a local folder, used in place |
| `repo` | a git URL; den clones it at `den build` |
| `name` | the template's name inside the definition; default: the key |
| `ref` | branch or tag of `repo`; default: the remote's HEAD |

Set exactly one of `repo` and `dir`. `ref` with `dir` is a load error.

**Share a template through git.** Put the location in a git repository and declare it with `repo:`:

```yaml
templates:
  node:
    repo: git@git.example.com:team/den-templates.git
    ref: main
```

den clones the repository into `~/.den/gen/.templates/<name>/` at `den build`. A `repo:` URL found
in a project `den.yaml` asks for confirmation on its first build (`--yes` skips the question); den
remembers the answer in `~/.den/state/v2/template-sources.yaml`. Without a terminal, such a URL is
refused unless you pass `--yes`. A URL in your global file needs no confirmation.

After a push to the template repository, run `den build <name> --pull`: a plain `den build` builds
the copy den already cloned. A project `den.yaml` that declares the template and the nests that use it lets a
whole team run `den build node` then `den up <nest>`.

## The definition file

`den-template.yaml` sits at the root of the location. One file can define several templates:

```yaml
schema: 1                      # required, must be 1
templates:
  node:
    from: claude-code-docker   # a sibling template of this file, an image, or empty
    kits: [./kits/node-kit]    # added to every VM on this template
    network:
      allow: [github.com]      # hosts every VM on this template may reach
    build:
      includes: [lib/common.sh]    # shell code prepended to every step
      steps: [steps/10-node.sh]    # one script per step, run in order
      network:
        allow: [registry.npmjs.org]   # hosts the build VM may reach, not the instances
```

- **Paths** are relative to the definition file and must stay inside the location. `..`, an absolute
  path, `~`, and a symlink that leaves the location are load errors. A kit entry whose first path
  component looks like a registry host (it contains `.` or `:`, or is `localhost`) is a kit
  reference that sbx resolves itself; write Docker Hub kits as `docker.io/acme/kit:1`.
- **`from`** is the template it builds on: a sibling template of the same file, an image (same rules
  as a nest's `template:`), or empty. Empty means the global `default_template`, which must then be
  an image.
- **Steps** run in the build VM through `bash -lc`, one `sbx exec` per step, with `build.includes`
  prepended. The output of each step is printed. A step that fails stops the build.
- The file is decoded strictly: an unknown key is a load error.

### Siblings

When `from:` names a sibling template, `den build <child>` first builds the sibling when it is not
built, when it was built from another source, when its sbx template is gone, or, for a `dir`
sibling, when its definition changed. A `repo` sibling built from another commit is rebuilt after a
fresh clone (`--pull`); otherwise den refuses and names `den build <child> --pull`. The sibling's
files count as part of the child's definition. `--force` on the child never reaches the sibling.

## `den build`

```bash
den build <template> [--pull] [--yes] [--force]
```

| Flag | Effect |
|---|---|
| `--pull` | clone the `repo` again instead of building the local copy |
| `--yes` | trust a `repo` declared in a project `den.yaml` without asking |
| `--force` | replace a template built from another source |

`den build` reads every step before it creates the build VM, so a missing script is refused at
once. It then provisions a throwaway VM named `<name>-build`, runs the steps, stops the VM, saves it
as the sbx template `<name>:v2`, removes the VM, and writes the template record
(`~/.den/state/v2/templates/<name>.yaml`: source, commit, definition hash, image digest). A failed
build removes the VM and keeps the previous build. A leftover `<name>-build` VM makes `den build` refuse: remove it with `sbx rm <name>-build`.

`den up` never clones and never builds. While a declared template is not built, `den up` refuses and
names `den build <template>`. Once it is built, `den up` prints a notice on stderr when the `repo`
has a newer commit (`den build <template> --pull`) or the `dir` definition changed
(`den build <template>`). The check takes at most 3 seconds and stays silent when the remote does
not answer.

## After a rebuild

An instance keeps the image it was created with. After a rebuild, `den up` on an existing instance
prints a warning that names the nest's `den.yaml` as changed, even if you did not edit it. Run `den rm <instance>`, then `den up <nest>`, to move it to the new
build.

## The image must keep the VM alive

sbx runs the image's own `ENTRYPOINT` as the VM's first process; Claude starts only when you attach.
If the entrypoint exits, the VM stops, and every `den build` step and every `den exec` dies with
**status 137**.

- `docker.io/docker/sandbox-templates:claude-code-docker` uses `tini --`, which keeps the VM up.
- `docker.io/sbx/claude` uses `claude`, which exits within seconds: do not use it as a template.

Check an image before you use it, if Docker is installed (sbx keeps its own image store, so pull the
image into Docker first):

```bash
docker pull <image>
docker image inspect --format '{{json .Config.Entrypoint}}' <image>
```

## The image must provide `jq`

At every VM start, den's `den-claude` kit runs `jq` in the VM. It merges two keys into Claude's own
files, so Claude opens at its prompt with no first-run screen:

- `hasCompletedOnboarding: true` in `~/.claude.json` removes the theme wizard;
- `skipDangerousModePermissionPrompt: true` in `~/.claude/settings.json` removes the bypass-mode warning.

The same step also merges the settings defaults that kits ship (see
[Default Claude settings from a kit](#default-claude-settings-from-a-kit)).

`docker.io/docker/sandbox-templates:claude-code-docker` ships `jq`. In an image without it, the
startup step fails with `den-claude: the image has no jq`; install `jq` in your template's build
steps. The `den build` VM does not run this step, so a `from:` image without `jq` still builds. What
sbx does with the VM after that failure is unverified.

## Default Claude settings from a kit

A kit can give every VM default values for Claude's `~/.claude/settings.json` without shipping that
file. Put one JSON object per file under `files/home/.den/claude-settings.d/` in the kit, for example
`files/home/.den/claude-settings.d/50-team.json`:

```json
{
  "statusLine": { "type": "command", "command": "my-tool statusline" }
}
```

At every VM start, after sbx copies the files of all kits, den merges these files into
`~/.claude/settings.json`:

- den reads the files in name order, and a later file wins over an earlier one. Start each name with
  a number (`10-…`, `50-…`) to set the order.
- A key already in `~/.claude/settings.json` keeps its value. A default fills only the keys that are
  missing. A personal kit that ships its own `~/.claude/settings.json` therefore overrides the
  defaults key by key, and keeps the defaults it does not set.
- Objects merge key by key, at every depth. An array or a single value replaces the default whole.
- A key you delete in the VM comes back at the next start. To turn a default off, give the key
  another value in your own settings.
- A file that is not a JSON object stops the start with
  `den-claude: <file> is not a JSON object: fix the kit that ships it`.

Do not ship `files/home/.claude/settings.json` from a template kit for this: a later kit that ships
the same file replaces it whole (unverified), so the template's values would not survive a personal kit.
The `den build` VM does not merge these files.
