package spawn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// denTest construit un ~/.den temporaire complet avec un dépôt git réel.
func denTest(t *testing.T) (denHome, repo string) {
	t.Helper()
	return denTestSSH(t, "  mode: agent-forward\n")
}

// egressUnHote est l'`egress:` de tous les tests qui ne jouent pas sur la
// dérive de configuration.
const egressUnHote = "  - github.com\n"

// denTestSSH permet de faire varier le bloc `ssh:` — c'est le seul levier qui
// ajoute un TROISIÈME workspace, et donc le seul qui rende leur ordre observable.
func denTestSSH(t *testing.T, blocSSH string) (denHome, repo string) {
	t.Helper()
	denHome = t.TempDir()
	repo = filepath.Join(t.TempDir(), "api")

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@exemple.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}

	ecrisConfig(t, denHome, blocSSH, egressUnHote)
	// Deux kits déclarés, pas zéro : sans eux, le mixin serait le seul `--kit`
	// de l'argv et « le mixin est layeré en dernier » se vérifierait tout seul.
	ecris(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nkits: [transverse]\nkit: devx-kit\n")
	ecris(t, filepath.Join(denHome, "nests", "api.yaml"), "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return denHome, repo
}

// ecrisConfig (ré)écrit le config.yaml du den home. Extrait de denTestSSH pour
// que les tests de dérive puissent RÉÉCRIRE la cascade entre deux spawns : c'est
// le seul moyen de reproduire une config qui a bougé sous une VM qui, elle, n'a
// pas bougé.
func ecrisConfig(t *testing.T, denHome, blocSSH, blocEgress string) {
	t.Helper()
	ecris(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    config_dir: `+filepath.Join(denHome, "agents", "claude")+`
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
`+blocSSH+`worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
egress:
`+blocEgress)
}

func ecris(t *testing.T, chemin, contenu string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(chemin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chemin, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

// policyInstantanee : mêmes réglages que le défaut, mais sur une horloge SIMULÉE
// que Sommeil fait avancer.
//
// Le patron naturel — un Sommeil sans effet posé à côté de time.Now — est un
// double CASSÉ : l'horloge n'avance alors pas d'un intervalle par tour, la
// boucle n'atteint jamais sa limite temporelle et sort sur sa borne en tours,
// avec une erreur « défaut de l'appelant » qui n'a rien d'un fail-closed. Les
// tests où tout passe au premier tour ne le voient même pas ; ceux où la policy
// bloque resteraient verts en ne prouvant plus rien. D'où le couple Sommeil /
// Maintenant partageant la même horloge, et l'assertion sur la CAUSE de l'échec
// dans TestSpawnNAttachePasSiLaPolicyNePasse.
func policyInstantanee() policy.Options {
	o := policy.OptionsDefaut()
	horloge := time.Now()
	o.Sommeil = func(d time.Duration) { horloge = horloge.Add(d) }
	o.Maintenant = func() time.Time { return horloge }
	return o
}

// depsTest : sbx factice qui répond « aucune sandbox » puis « tout autorisé ».
func depsTest() (*sbx.Fake, Deps) {
	return depsAvecVerdict(`{"allowed": true}`)
}

func depsAvecVerdict(verdict string) (*sbx.Fake, Deps) {
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{
			"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
		},
		Defaut: sbx.Reponse{Sortie: []byte(verdict)},
	}
	return f, Deps{
		Sbx:    f,
		Git:    worktree.NewGit(),
		Policy: policyInstantanee(),
		Sortie: io.Discard,
	}
}

func TestSpawnSequenceNominale(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if !f.AAppele("create", "--name", "api", "--template", "devx:v1") {
		t.Errorf("un create doit avoir eu lieu ; appels : %v", f.Appels)
	}
	if !f.AAppele("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("le settle-loop doit avoir tourné sur l'egress de la cascade ; appels : %v", f.Appels)
	}
	// AAttache et non AAppele : Appels confond Run et Attach, et un Run à la
	// place d'un Attach rendrait à l'utilisateur un shell muet, sans tty.
	if !f.AAttache("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("l'attache doit avoir eu lieu ; attaches : %v", f.Attaches)
	}
}

// L'ordre est une propriété de sûreté : attacher avant que la policy soit
// posée, c'est exactement le « ça marche à moitié » que le spec §7 interdit.
func TestSpawnAttacheApresLeSettleLoop(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	iCreate, iPolicy, iExec := -1, -1, -1
	for i, a := range f.Appels {
		if len(a) > 0 && a[0] == "create" && iCreate < 0 {
			iCreate = i
		}
		if len(a) > 0 && a[0] == "policy" && iPolicy < 0 {
			iPolicy = i
		}
		if len(a) > 0 && a[0] == "exec" {
			iExec = i
		}
	}
	if iCreate < 0 || iPolicy < 0 || iExec < 0 {
		t.Fatalf("create (%d), policy (%d) et exec (%d) doivent tous avoir eu lieu ; appels : %v",
			iCreate, iPolicy, iExec, f.Appels)
	}
	if !(iCreate < iPolicy && iPolicy < iExec) {
		t.Errorf("ordre attendu create (%d) < policy (%d) < attache (%d) ; appels : %v",
			iCreate, iPolicy, iExec, f.Appels)
	}
}

// Fail-closed de bout en bout : policy bloquée ⇒ aucune attache.
func TestSpawnNAttachePasSiLaPolicyNePasse(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsAvecVerdict(`{"allowed": false}`)

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("une policy qui ne passe pas doit faire échouer le spawn")
	}
	// Le spawn doit échouer pour la BONNE raison. Settle a deux sorties en
	// erreur : le fail-closed (ce qu'on teste) et la borne en tours, réservée
	// aux horloges qui mentent. Sans cette distinction, un double d'horloge
	// cassé rendrait ce test vert sans que le fail-closed soit jamais exercé.
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("l'échec doit être le fail-closed de la policy ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "défaut de l'appelant") {
		t.Errorf("Settle a buté sur sa borne en tours (horloge de test cassée), "+
			"pas sur le fail-closed ; obtenu : %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("aucune attache ne doit avoir lieu ; attaches : %v", f.Attaches)
	}
}

// Spawn-or-attach (spec §11) : un nom déjà vivant n'est pas une erreur.
func TestSpawnAttacheSansRecreerSiLaSandboxExiste(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := depsTest()
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if f.AAppele("create") {
		t.Errorf("aucun create ne doit avoir lieu sur une sandbox vivante ; appels : %v", f.Appels)
	}
	// Le settle-loop doit tourner AUSSI sur cette branche — c'est celle
	// qu'emprunte tout spawn après le premier. Sans cette assertion, un
	// settle-loop replié dans la branche `create` laisse la suite verte
	// pendant que `den api` attache un shell sans avoir vérifié la policy.
	if !f.AAppele("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("le settle-loop doit tourner aussi sur une sandbox vivante ; appels : %v", f.Appels)
	}
	if !f.AAttache("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("l'attache doit avoir lieu ; attaches : %v", f.Attaches)
	}
}

// C'est le nom COMPLET, worktree inclus, qui doit être cherché parmi les
// sandboxes vivantes. Chercher `o.Nest` reviendrait à confondre `api` et
// `api.feat12` : indétectable tant que le seul test à sandbox vivante n'a pas
// de worktree, puisque les deux valeurs y coïncident.
//
// Le `-w` attendu est `/w`, le workspace que la VM déclare — et non le chemin de
// worktree que la cascade recalculerait. Cette attente-là appartient au test
// dédié ci-dessous (TestSpawnAttacheDansLeWorkdirRemonteParLaVM) ; ici, elle
// n'est qu'une conséquence.
func TestSpawnChercheLeNomWorktreeeParmiLesSandboxesVivantes(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["/w"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if f.AAppele("create") {
		t.Errorf("aucun create ne doit avoir lieu sur api.feat12 vivante ; appels : %v", f.Appels)
	}
	if !f.AAttache("exec", "-it", "-w", "/w", "api.feat12", "bash", "-l") {
		t.Errorf("l'attache doit viser api.feat12 ; attaches : %v", f.Attaches)
	}
}

// D2 — le `-w` d'une sandbox VIVANTE vient du workspace que la VM MONTE, jamais
// d'un chemin recalculé depuis la configuration courante.
//
// Une VM monte les workspaces de son `sbx create` d'origine : si le premier repo
// du nest a changé de chemin depuis (ou si `-w` a été ajouté), le chemin
// recalculé n'existe tout simplement pas dedans, et `sbx exec -w` échoue — ou
// pire, atterrit ailleurs. sbx.Sandbox.Workdir existe depuis la tâche 8
// exactement pour ça.
//
// Le `:ro` du fixture n'est pas décoratif : il distingue `b.Workdir()` (qui le
// retire) de `b.Workspaces[0]` (qui le garderait), et sépare donc les deux
// implémentations possibles.
func TestSpawnAttacheDansLeWorkdirRemonteParLaVM(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := depsTest()
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running",` +
			`"workspaces":["/monte/par/la/vm:ro","/profil"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAttache("exec", "-it", "-w", "/monte/par/la/vm", "api", "bash", "-l") {
		t.Errorf("le -w doit venir du workspace de la VM ; attaches : %v", f.Attaches)
	}
	// Et surtout PAS le chemin que la cascade recalcule : c'est lui, le défaut.
	for _, a := range f.Attaches {
		if slices.Contains(a, repo) {
			t.Errorf("le -w ne doit pas être le chemin recalculé %s ; attache : %v", repo, a)
		}
	}
}

// D1 — une sandbox trouvée mais qui NE TOURNE PAS n'est pas une sandbox vivante.
//
// Avant ce contrôle, sbx.Existe ne rendait qu'un booléen et jetait le Statut :
// `den api` sur une VM `exited` affichait « déjà vivante : attache » puis
// lançait un `sbx exec` dans une VM arrêtée.
//
// La liste blanche (« running » et rien d'autre) est délibérée et FAIL-CLOSED :
// les autres valeurs de `status` que sbx peut émettre ne sont pas connues ici
// (sbx n'est pas installable sur cette machine). Une liste NOIRE
// — {"exited","stopped"} — attacherait dans tout statut qu'une version
// ultérieure de sbx introduirait. Le prix assumé : un statut transitoire de
// démarrage ferait échouer un `den api` lancé trop tôt, avec un message qui
// nomme le statut lu.
func TestSpawnRefuseUneSandboxQuiNeTournePas(t *testing.T) {
	for _, statut := range []string{"exited", "stopped", "paused", "Running", ""} {
		t.Run("statut="+statut, func(t *testing.T) {
			denHome, _ := denTest(t)
			f, d := depsTest()
			f.Reponses["ls --json"] = sbx.Reponse{
				Sortie: []byte(`{"sandboxes":[{"name":"api","status":"` + statut +
					`","workspaces":["/w"]}]}`),
			}

			err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
			if err == nil {
				t.Fatalf("un statut %q ne doit pas être traité comme vivant", statut)
			}
			// Le message doit rendre le statut LU : sans lui, l'utilisateur ne
			// sait pas de quoi den se plaint ni quoi taper ensuite.
			if !strings.Contains(err.Error(), statut) || !strings.Contains(err.Error(), "running") {
				t.Errorf("le message doit rendre le statut lu et celui attendu ; obtenu : %v", err)
			}
			if !strings.Contains(err.Error(), "api") {
				t.Errorf("le message doit nommer la sandbox ; obtenu : %v", err)
			}
			if len(f.Attaches) != 0 {
				t.Errorf("aucune attache dans une VM arrêtée ; attaches : %v", f.Attaches)
			}
			// Ni create (le nom est pris) ni settle-loop : den s'arrête net.
			if f.AAppele("create") {
				t.Errorf("aucun create ne doit être tenté sur un nom déjà pris ; appels : %v", f.Appels)
			}
		})
	}
}

// D3 — rien ne réapplique un mixin à une VM en marche.
//
// Un `egress:` RÉTRÉCI passe donc le settle-loop en silence : la policy large
// que la VM porte depuis son create autorise évidemment la liste étroite qu'on
// lui soumet. L'utilisateur croit sa sandbox resserrée alors qu'elle est restée
// ouverte. (Le sens inverse, élargir, échoue proprement sur le settle-loop.)
//
// PIÈGE D'ORDRE, et raison d'être de ce test : Spawn RÉÉCRIT le mixin à chaque
// passage (étape 5), AVANT la branche spawn-or-attach. Une comparaison faite
// après cette écriture compare le mixin à lui-même et ne détecte JAMAIS rien,
// avec une suite parfaitement verte. C'est la mutation tueuse : déplacer le
// LisMixin après le EcrisMixin.
func TestSpawnAvertitQuandLaConfigADeriveSousLaSandbox(t *testing.T) {
	denHome, repo := denTest(t)
	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	// Premier passage : la sandbox est créée, et c'est CE mixin-là qu'elle porte.
	f, d := depsTest()
	journal := &bytes.Buffer{}
	d.Sortie = journal
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("premier spawn : erreur inattendue : %v", err)
	}
	if !f.AAppele("create", "--name", "api") {
		t.Fatalf("le premier spawn doit créer la sandbox ; appels : %v", f.Appels)
	}

	// La configuration se resserre. La VM, elle, ne bouge pas.
	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}
	journal.Reset()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn : erreur inattendue : %v", err)
	}
	sortie := journal.String()
	if !strings.Contains(sortie, "api.anthropic.com") {
		t.Errorf("l'avertissement doit NOMMER l'egress qui a disparu de la config ;\n%s", sortie)
	}
	if !strings.Contains(sortie, "attention") {
		t.Errorf("la dérive doit être annoncée comme un avertissement ;\n%s", sortie)
	}
	// On AVERTIT, on ne refuse pas : refuser casserait un `den api` qui marchait
	// hier pour un YAML anodin (décision arrêtée, cf. handoff T12 §6).
	if len(f.Attaches) != 1 {
		t.Errorf("la dérive avertit puis attache ; attaches : %v", f.Attaches)
	}
}

// Le pendant indispensable : sans dérive, aucun avertissement. Un avertissement
// qui sort à CHAQUE attache ne serait plus lu du tout — et rendrait le test
// ci-dessus vert sans rien prouver.
func TestSpawnNAvertitPasSansDerive(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := depsTest()
	journal := &bytes.Buffer{}
	d.Sortie = journal
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("premier spawn : erreur inattendue : %v", err)
	}

	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}
	journal.Reset()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn : erreur inattendue : %v", err)
	}
	if sortie := journal.String(); strings.Contains(sortie, "attention") {
		t.Errorf("une configuration inchangée ne doit rien signaler ;\n%s", sortie)
	}
}

// La branche CREATE ne doit jamais avertir : c'est le create qui POSE le mixin,
// il ne peut pas avoir dérivé de lui-même.
//
// Le cas piégeux est un cache/ qui survit à la sandbox : le spec §3 déclare
// cache/ reconstructible et den ne le purge pas, donc un `sbx rm` suivi d'un
// `den api` retrouve le mixin de la sandbox DÉFUNTE sur le disque. Un
// avertissement hissé hors de la branche « vivante » sortirait là, sur une
// sandbox qui reçoit pourtant la configuration exacte.
func TestSpawnNAvertitPasSurLaBrancheCreate(t *testing.T) {
	denHome, _ := denTest(t)
	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	f, d := depsTest()
	journal := &bytes.Buffer{}
	d.Sortie = journal
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("premier spawn : erreur inattendue : %v", err)
	}

	// La config change, la sandbox a disparu (`sbx rm`), le cache reste.
	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	journal.Reset()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("second spawn : erreur inattendue : %v", err)
	}
	if !f.AAppele("create", "--name", "api") {
		t.Fatalf("le second spawn doit créer ; appels : %v", f.Appels)
	}
	if sortie := journal.String(); strings.Contains(sortie, "attention") {
		t.Errorf("un create pose le mixin : rien à signaler ;\n%s", sortie)
	}
}

// Le mixin sur disque est la RÉFÉRENCE du `create` : il ne doit pas être
// réécrit quand la sandbox est déjà vivante.
//
// Sinon la dérive s'efface elle-même : le premier `den api` avertit, réécrit la
// référence au passage, et le second se tait — alors que la VM, elle, n'a
// toujours pas bougé. C'est le défaut le plus coûteux du lot, parce qu'il rend
// la détection MUETTE exactement dans le cas où elle sert, sans jamais rien
// faire échouer.
func TestSpawnNeReecritPasLeMixinDUneSandboxVivante(t *testing.T) {
	denHome, repo := denTest(t)
	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	f, d := depsTest()
	journal := &bytes.Buffer{}
	d.Sortie = journal
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("premier spawn : erreur inattendue : %v", err)
	}
	spec := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	reference, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("le create doit avoir écrit %s : %v", spec, err)
	}

	ecrisConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	// Deux attaches d'affilée : la seconde est celle qui trahit une référence
	// écrasée par la première.
	for tour := 1; tour <= 2; tour++ {
		journal.Reset()
		if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
			t.Fatalf("tour %d : erreur inattendue : %v", tour, err)
		}
		apres, err := os.ReadFile(spec)
		if err != nil {
			t.Fatalf("tour %d : lecture de %s : %v", tour, spec, err)
		}
		if string(apres) != string(reference) {
			t.Fatalf("tour %d : le mixin de référence a été réécrit sur une sandbox vivante ;\n"+
				"avant :\n%s\naprès :\n%s", tour, reference, apres)
		}
		if !strings.Contains(journal.String(), "api.anthropic.com") {
			t.Errorf("tour %d : la dérive doit rester signalée ;\n%s", tour, journal.String())
		}
	}
}

// Une référence ILLISIBLE n'est pas une absence de référence. L'absence est
// normale (premier spawn, ou cache/ purgé) et se tait ; un spec.yaml corrompu
// veut dire que den ne peut PAS répondre à la question, et se taire là-dessus
// laisserait croire que la configuration n'a pas dérivé.
func TestSpawnSignaleUneDeriveNonVerifiable(t *testing.T) {
	denHome, repo := denTest(t)
	ecris(t, filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml"), "\tpas du yaml")

	f, d := depsTest()
	journal := &bytes.Buffer{}
	d.Sortie = journal
	f.Reponses["ls --json"] = sbx.Reponse{
		Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	sortie := journal.String()
	if !strings.Contains(sortie, "non vérifiable") {
		t.Errorf("une référence illisible doit être signalée comme telle ;\n%s", sortie)
	}
	// Et l'attache a bien lieu : une dérive invérifiable ne bloque pas.
	if len(f.Attaches) != 1 {
		t.Errorf("l'attache doit avoir lieu ; attaches : %v", f.Attaches)
	}
}

func TestSpawnAvecWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if !f.AAppele("create", "--name", "api.feat12") {
		t.Errorf("le nom doit porter le worktree ; appels : %v", f.Appels)
	}
	chemin := filepath.Join(denHome, "worktrees", "feat12", "api")
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit exister en %s : %v", chemin, err)
	}
	// Et c'est le worktree, pas le repo, qui est monté — en premier positionnel.
	ws := workspacesDe(appelCommencantPar(f, "create"))
	if len(ws) == 0 || ws[0] != chemin {
		t.Errorf("le worktree doit être le premier workspace ; workspaces = %v", ws)
	}
	// Le nom worktreeé doit traverser TOUTE la séquence : un settle-loop scopé
	// sur « api » validerait la policy d'une autre sandbox que celle créée.
	if !f.AAppele("policy", "check", "network", "--sandbox", "api.feat12", "--json", "github.com") {
		t.Errorf("le settle-loop doit être scopé sur api.feat12 ; appels : %v", f.Appels)
	}
	if !f.AAttache("exec", "-it", "-w", chemin, "api.feat12", "bash", "-l") {
		t.Errorf("l'attache doit ouvrir dans le worktree ; attaches : %v", f.Attaches)
	}
}

// VERROU : le PREMIER workspace doit être le repo (ou son worktree).
//
// sbx.Sandbox.Workdir prend le premier workspace de `sbx ls` pour répertoire de
// travail, et rien à SON niveau ne peut vérifier que l'appelant l'a bien rangé
// en tête — c'est ici, à l'unique endroit qui compose cette liste, que le
// contrat doit être verrouillé. Le profil agent (toujours présent) et le dossier
// SSH (mode « mount ») sont les deux candidats naturels à le doubler.
func TestSpawnMonteLeRepoAvantLeProfilAgentEtSSH(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), "ssh")
	denHome, repo := denTestSSH(t, "  mode: mount\n  dir: "+sshDir+"\n")
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	ws := workspacesDe(appelCommencantPar(f, "create"))
	attendu := []string{repo, filepath.Join(denHome, "agents", "claude"), sshDir}
	if !slices.Equal(ws, attendu) {
		t.Errorf("workspaces = %v, attendu %v", ws, attendu)
	}
	// Même contrat vu de l'autre bout : c'est ce premier workspace qui devient
	// le -w de l'attache.
	if !f.AAttache("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("l'attache doit ouvrir dans le repo ; attaches : %v", f.Attaches)
	}
}

// Un nom de worktree issu d'un nom de branche doit être refusé AVANT tout
// effet de bord : ni worktree créé, ni sandbox.
func TestSpawnRefuseUnWorktreeNonSandboxable(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feature/123"}, d)
	if err == nil {
		t.Fatal("un worktree contenant / doit être refusé")
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err == nil {
		t.Error("aucun worktree ne doit avoir été créé")
	}
}

// Spec §11 : « Chemin repo introuvable → stop AVANT tout create ».
func TestSpawnStoppeAvantCreateSiUnRepoManque(t *testing.T) {
	denHome, repo := denTest(t)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err == nil {
		t.Fatal("un repo introuvable doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), repo) {
		t.Errorf("le message doit nommer le repo manquant ; obtenu : %v", err)
	}
	// Aucun appel du tout, et pas seulement aucun create : le contrôle doit
	// précéder même le `sbx ls` du spawn-or-attach.
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
	// Et aucun effet de bord SUR DISQUE. Le spec §11 écrit « stop avant tout
	// create », mais l'intention est « avant tout effet de bord » : sans ces
	// deux contrôles, déplacer la garde juste avant le bloc spawn-or-attach
	// laisserait derrière elle le profil agent et le mixin, tout en gardant
	// l'assertion sur f.Appels vraie.
	for _, chemin := range []string{
		filepath.Join(denHome, "agents", "claude"),
		filepath.Join(denHome, "cache", "mixins"),
	} {
		if _, err := os.Stat(chemin); err == nil {
			t.Errorf("aucun effet de bord ne doit avoir eu lieu, or %s existe", chemin)
		}
	}
}

// Le profil agent est monté RW : un config_dir vide monterait un chemin vide, et
// le message d'un MkdirAll("") ne désignerait rien. Contrôlé avant tout effet de
// bord, et le message nomme le fichier à corriger.
func TestSpawnRefuseUnAgentSansConfigDir(t *testing.T) {
	denHome, _ := denTest(t)
	ecris(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	f, d := depsTest()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d)
	if err == nil {
		t.Fatal("un agent sans config_dir doit faire échouer le spawn")
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("le message doit nommer le fichier fautif ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err == nil {
		t.Error("aucun worktree ne doit avoir été créé")
	}
}

func TestSpawnDetachNAttachePas(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("create", "--name", "api") {
		t.Errorf("le create doit avoir lieu ; appels : %v", f.Appels)
	}
	if !f.AAppele("policy", "check", "network", "--sandbox", "api") {
		t.Errorf("--detach ne dispense pas du settle-loop ; appels : %v", f.Appels)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("--detach ne doit pas attacher ; attaches : %v", f.Attaches)
	}
}

// Le profil agent est monté RW et doit exister : sbx créerait sinon un dossier
// vide au mount, et l'agent repartirait de zéro à chaque spawn.
func TestSpawnCreeLeProfilAgent(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err != nil {
		t.Errorf("le config_dir de l'agent doit exister : %v", err)
	}
}

func TestSpawnEcritLeMixin(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	spec := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	contenu, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("le mixin doit être écrit en %s : %v", spec, err)
	}
	if !strings.Contains(string(contenu), "github.com") {
		t.Errorf("le mixin doit porter l'egress de la cascade :\n%s", contenu)
	}
	// Ordre de layering complet : kits transverses, puis kit de la stack, puis
	// le mixin — TOUJOURS en dernier (le dispatcher sbx fait `exit $rc` au
	// premier échec et priverait les kits suivants de leurs startup commands).
	stackDir := filepath.Join(denHome, "stacks", "devx")
	attendu := []string{
		filepath.Join(stackDir, "transverse"),
		filepath.Join(stackDir, "devx-kit"),
		filepath.Dir(spec),
	}
	if k := kitsDe(appelCommencantPar(f, "create")); !slices.Equal(k, attendu) {
		t.Errorf("--kit = %v, attendu %v", k, attendu)
	}
}

// Les trois options de cascade doivent atteindre nest.Resolve.
//
// Chacune est exercée par une valeur INVALIDE : c'est le seul moyen d'obtenir,
// sans sbx, un message qui dépend de la VALEUR passée — donc la preuve qu'elle
// a traversé. Une option muette (`Only` ou `Agent` non transmis) fait retomber
// le spawn sur le défaut et réussit en silence : `--agent claude-next`
// monterait le profil de l'agent par défaut et écrirait SES variables
// d'environnement dans le mixin, sans un mot.
func TestSpawnPropageLesOptionsDeCascade(t *testing.T) {
	cas := []struct {
		nom     string
		options Options
		attendu string
	}{
		{"Without", Options{Nest: "api", Without: []string{"inconnu"}}, `--without : repo "inconnu"`},
		{"Only", Options{Nest: "api", Only: []string{"inconnu"}}, `--only : repo "inconnu"`},
		{"Agent", Options{Nest: "api", Agent: "inconnu"}, `agent "inconnu"`},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			denHome, _ := denTest(t)
			f, d := depsTest()

			err := Spawn(context.Background(), denHome, c.options, d)
			if err == nil {
				t.Fatalf("%s avec une valeur inconnue doit faire échouer le spawn", c.nom)
			}
			if !strings.Contains(err.Error(), c.attendu) {
				t.Errorf("%s n'atteint pas la cascade (attendu %q) ; obtenu : %v", c.nom, c.attendu, err)
			}
			if len(f.Appels) != 0 {
				t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
			}
		})
	}
}

// L'échec de `sbx create` doit être recontextualisé. Le message brut d'Exec.Run
// est préfixé de l'argv COMPLET — une ligne géante avec tous les --kit et tous
// les workspaces — dans laquelle l'étape qui a échoué devient illisible.
func TestSpawnNommeLEtapeQuandLeCreateEchoue(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()
	f.Defaut = sbx.Reponse{Err: errors.New("boum")}

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("un create en échec doit faire échouer le spawn")
	}
	if !strings.Contains(err.Error(), "création de la sandbox api") {
		t.Errorf("le message doit nommer l'étape et la sandbox ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "boum") {
		t.Errorf("le message doit conserver la cause ; obtenu : %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("aucune attache ne doit avoir lieu ; attaches : %v", f.Attaches)
	}
}

// Spawn refuse un den home relatif AVANT tout effet de bord.
//
// C'est cet invariant — garanti par nest.Resolve — qui rend `denHome` et
// `r.DenHome` interchangeables, et donc INDÉTECTABLE par construction le choix
// de celui qu'on passe à EcrisMixin : Resolve pose r.DenHome = denHome dès lors
// qu'il est absolu, et refuse tout le reste. Ce qui se teste ici n'est pas le
// choix, c'est l'invariant qui le rend sans conséquence.
func TestSpawnRefuseUnDenHomeRelatif(t *testing.T) {
	denHome, _ := denTest(t)
	t.Chdir(filepath.Dir(denHome))
	f, d := depsTest()

	err := Spawn(context.Background(), filepath.Base(denHome), Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("un den home relatif doit faire échouer le spawn")
	}
	if !strings.Contains(err.Error(), "non absolu") {
		t.Errorf("le message doit nommer la cause ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
}

// Une Sortie nil ne doit pas paniquer au milieu d'un spawn : l'appelant qui
// oublie de la remplir a déjà, à ce stade, une sandbox créée et démarrée.
func TestSpawnTolereUneSortieNil(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()
	d.Sortie = nil

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("create", "--name", "api") {
		t.Errorf("le spawn doit s'être déroulé ; appels : %v", f.Appels)
	}
}

func appelCommencantPar(f *sbx.Fake, tete string) []string {
	for _, a := range f.Appels {
		if len(a) > 0 && a[0] == tete {
			return a
		}
	}
	return nil
}

// kitsDe extrait les valeurs des `--kit` d'un argv, dans l'ordre.
func kitsDe(argv []string) []string {
	var out []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// workspacesDe extrait les positionnels d'un `sbx create`, c'est-à-dire tout ce
// qui suit l'agent positionnel.
func workspacesDe(argv []string) []string {
	i := slices.Index(argv, sbx.AgentPositionnel)
	if i < 0 {
		return nil
	}
	return argv[i+1:]
}
