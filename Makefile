# Where the greatkit plugin repo is checked out. Override on the command line
# if yours lives elsewhere: make sync-greatkit GREATKIT=/path/to/greatkit
GREATKIT ?= ../../Kampn/greatplugin

# Refresh the vendored copy of greatkit under .claude/.
#
# Cloud sessions cannot install the plugin from its marketplace, so the skills
# and agents are committed here instead — see
# docs/superpowers/decisions/2026-07-30-plugins-in-cloud-sessions.md.
# Repo-local agents resolve without a plugin prefix, so every `greatkit:foo`
# agent reference is rewritten to `foo` on the way in.
sync-greatkit:
	@test -d "$(GREATKIT)/skills" || { echo "greatkit not found at $(GREATKIT) — set GREATKIT=<path>"; exit 1; }
	rsync -a --delete "$(GREATKIT)/skills/greatship" "$(GREATKIT)/skills/greatreview" "$(GREATKIT)/skills/greatfix" .claude/skills/
	rsync -a --delete "$(GREATKIT)/agents/" .claude/agents/
	@grep -rl 'greatkit:' .claude/skills | while read -r f; do sed -i '' 's/greatkit://g' "$$f"; done
	@grep -rn 'greatkit:' .claude/skills .claude/agents && { echo "prefix rewrite incomplete"; exit 1; } || true
	@for f in .claude/skills/*/*.workflow.js; do node --check "$$f" || exit 1; done
	@echo "vendored greatkit from $(GREATKIT)"

test:
	go test -count=1 ./...

typecheck:
	go build ./...

lint:
	go vet ./... && test -z "$$(gofmt -l .)"

.PHONY: test typecheck lint sync-greatkit
