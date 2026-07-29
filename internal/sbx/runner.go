package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Runner est le SEUL point de contact de den avec la CLI sbx. Tout passe par
// là, ce qui rend l'intégralité du reste testable sans microVM — sbx n'est même
// pas installé sur la machine de développement.
//
// Deux méthodes, parce que les deux usages sont irréconciliables :
//   - Run capture stdout pour le parser (`ls --json`, `policy check --json`).
//   - Attach branche les tty du processus courant pour rendre un shell
//     interactif à l'utilisateur (`exec -it … bash -l`) ; il n'y a rien à
//     capturer, et capturer casserait l'interactivité.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	Attach(ctx context.Context, args ...string) error
}

// Exec est l'implémentation réelle, adossée au binaire sbx du PATH.
//
// ENVIRONNEMENT : cmd.Env est laissé nil dans Run comme dans Attach, et c'est
// une décision, pas un oubli. nil ⇒ le process hérite de l'environnement de
// den, et c'est le SEUL support de `ssh.mode: agent-forward` — le défaut de la
// configuration (internal/config/config.go), qui n'ajoute ni argument à l'argv
// de `sbx create` ni entrée au mixin, et repose entièrement sur le fait que
// SSH_AUTH_SOCK atteigne le process sbx.
//
// Renseigner cmd.Env ici, pour n'importe quelle raison, couperait donc l'accès
// SSH de TOUTES les sandboxes. Les deux tests
// TestExec{Run,Attach}TransmetLEnvironnementDeDen sont ce qui l'interdit —
// mesuré avant de les écrire, un `cmd.Env = []string{"PATH=…"}` posé sur Run
// laissait la suite ENTIÈRE verte. Le précédent existe dans le projet :
// internal/worktree pose bien un cmd.Env, pour neutraliser la configuration git.
//
// Ce qui est prouvé s'arrête au process : que sbx propage ensuite ce socket
// DANS la microVM n'est pas vérifiable sans sbx, et reste une hypothèse du spec.
type Exec struct {
	Bin string
	// DelaiDeDrainage borne le temps laissé aux tubes pour finir de se vider,
	// une fois le process sorti OU le contexte annulé. Zéro ⇒
	// delaiDrainageDefaut. Réglable pour que les tests de la borne durent des
	// millisecondes plutôt que des secondes.
	//
	// Nommé d'après ce qu'il borne, et non « DelaiApresAnnulation » comme dans
	// une première version : ce nom-là affirmait que seule l'annulation armait
	// la borne, ce qui est mesuré faux et a masqué une régression.
	DelaiDeDrainage time.Duration
}

// NewExec construit un Runner réel. bin vide ⇒ « sbx » (résolu via le PATH).
func NewExec(bin string) Runner {
	if bin == "" {
		bin = "sbx"
	}
	return &Exec{Bin: bin}
}

// ErreurExec est l'erreur renvoyée par Run en cas d'échec. Exportée (et pas un
// simple message formaté) parce que policy et spawn ont besoin d'inspecter le
// résultat, pas seulement de l'afficher. Les trois propriétés ci-dessous sont
// tenues par un test chacune dans runner_test.go — aucune n'est déduite de la
// documentation d'os/exec :
//   - errors.As pour retrouver le *exec.ExitError sous-jacent et son code de
//     sortie (create échoué, il faut dire pourquoi) ;
//   - errors.Is(err, exec.ErrNotFound) pour distinguer « sbx absent du PATH »
//     d'un échec applicatif quelconque (doctor.go fait déjà cette distinction
//     via LookPath ; le runner doit pouvoir la faire aussi) ;
//   - errors.Is(err, context.Canceled) — et de même DeadlineExceeded — pour
//     reconnaître un Ctrl-C, que le contexte soit annulé AVANT le démarrage du
//     process ou PENDANT son exécution.
//
// Cette troisième propriété ne s'obtient PAS toute seule, et le champ
// Annulation est ce qui la porte. Mesuré (trois fois, dont ici en Go 1.26) :
// quand le contexte est annulé pendant l'exécution, os/exec tue le process et
// `Cmd.Wait` PRÉFÈRE l'erreur du process à celle du contexte — cmd.Run rend un
// *exec.ExitError « signal: killed » qui n'enveloppe ni Canceled ni
// DeadlineExceeded. Seule l'annulation survenue avant le démarrage remonte
// telle quelle (Cmd.Start rend ctx.Err() sans lancer le process). Run relève
// donc ctx.Err() lui-même et le joint à la chaîne.
//
// D'où Unwrap : la chaîne d'erreurs de cmd.Run() doit survivre intacte — les
// deux premières propriétés en dépendent — en plus du motif d'annulation et du
// message qui, lui, intègre stderr pour rester lisible par un humain.
type ErreurExec struct {
	Bin    string
	Args   []string
	Stderr string
	Err    error
	// Annulation porte le ctx.Err() observé au retour de cmd.Run, nil si le
	// contexte n'y est pour rien. C'est la SEULE source de Canceled /
	// DeadlineExceeded dans la chaîne quand le process a été tué.
	Annulation error
}

// messageBinaireIntrouvable rend, en français et avec un remède, le seul échec
// d'exécution que l'utilisateur puisse réparer lui-même : le binaire n'est pas
// installé.
//
// C'est l'échec de PREMIER CONTACT — sbx pas encore posé sur la machine — et il
// frappe les quatre commandes qui touchent sbx (`den ls`, `den <nest>`,
// `den sh`, `den rm`), toutes passant par Ls avant quoi que ce soit d'autre.
// Sans ce traitement, la première ligne qu'un nouvel utilisateur voit est celle
// d'os/exec : « exec: "sbx": executable file not found in $PATH » — en anglais,
// et sans dire quoi faire.
//
// `den doctor` est nommé parce qu'il diagnostique EXACTEMENT ce cas
// (doctor.go, étape 1, via exec.LookPath) et qu'il dira du même coup si le
// reste de l'installation tient. L'argv n'est PAS rendu : quand le binaire
// manque, aucun argument n'aurait changé quoi que ce soit, et le citer noierait
// le seul fait qui compte.
func messageBinaireIntrouvable(bin string) string {
	return fmt.Sprintf(
		"« %s » est introuvable dans le PATH — installe-le, puis vérifie ton installation avec `den doctor`",
		bin)
}

func (e *ErreurExec) Error() string {
	// AVANT le motif d'annulation : un contexte annulé sur un binaire absent
	// reste, pour l'utilisateur, un binaire absent — et c'est l'annulation qui
	// serait la conséquence, jamais la cause.
	if errors.Is(e.Err, exec.ErrNotFound) {
		return messageBinaireIntrouvable(e.Bin)
	}
	detail := e.Stderr
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if e.Annulation != nil {
		// Le motif d'annulation passe AVANT le détail, et ne le remplace pas :
		// « signal: killed » seul se lit comme un crash de sbx, mais le jeter
		// perdrait le stderr que sbx a eu le temps d'écrire.
		return fmt.Sprintf("%s %s : %s : %s",
			e.Bin, strings.Join(e.Args, " "), motifAnnulation(e.Annulation), detail)
	}
	return fmt.Sprintf("%s %s : %s", e.Bin, strings.Join(e.Args, " "), detail)
}

// motifAnnulation traduit les deux motifs d'annulation de la stdlib. Le défaut
// rend l'erreur telle quelle : un contexte peut porter une cause applicative
// (context.WithCancelCause), et l'inventer en français la perdrait.
func motifAnnulation(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "interrompu (délai dépassé)"
	case errors.Is(err, context.Canceled):
		return "interrompu (annulé)"
	default:
		return fmt.Sprintf("interrompu (%v)", err)
	}
}

// Unwrap rend une SLICE (et non l'unique Err d'avant) : errors.Is doit trouver
// le motif d'annulation ET errors.As le *exec.ExitError, qui sont deux erreurs
// distinctes. La slice ne contient jamais de nil, ce que le contrat d'errors
// interdit.
func (e *ErreurExec) Unwrap() []error {
	var chaine []error
	if e.Err != nil {
		chaine = append(chaine, e.Err)
	}
	if e.Annulation != nil {
		chaine = append(chaine, e.Annulation)
	}
	return chaine
}

// delaiDrainageDefaut borne le temps laissé aux tubes pour finir de se vider.
//
// PORTÉE EXACTE, corrigée après avoir été écrite fausse : le minuteur de
// WaitDelay démarre quand le contexte est annulé **OU dès que Wait constate la
// sortie du process**, au premier des deux. Il ne se déclenche donc PAS
// « seulement après une annulation ». Ce qu'il borne, c'est l'attente d'un
// descendant qui a hérité des tubes et les tient ouverts après la sortie du
// process — pas la durée du process lui-même.
//
// Mesuré, contexte jamais annulé, `&Exec{Bin: "sh"}` :
//
//	"sleep 4; echo fini"          → 4,01 s, nil        (un `create` lent n'est PAS borné)
//	"echo demarree; sleep 30 &"   → borné, succès      (descendant qui survit)
//	"echo x; sleep 30 & exit 3"   → borné, exit 3      (l'échec réel remonte tout de suite)
//
// La première ligne est la garantie qui compte : tant que sbx n'est pas sorti,
// rien ne le borne. Voir Run pour le traitement d'ErrWaitDelay.
const delaiDrainageDefaut = 2 * time.Second

// delaiEffectif applique le défaut. Fonction séparée pour que « la valeur nulle
// d'Exec est la valeur SÛRE » soit vérifiable sans faire dormir la suite : la
// borne elle-même se prouve en exécutant un process, le choix du défaut non.
func delaiEffectif(d time.Duration) time.Duration {
	if d <= 0 {
		return delaiDrainageDefaut
	}
	return d
}

// drainageEcourteSurSucces dit si l'erreur rendue par cmd.Run n'est QUE le
// drainage écourté d'un process qui a par ailleurs réussi.
//
// Fonction séparée parce que deux de ses trois conditions portent sur des états
// qu'on ne sait pas provoquer à la demande depuis un test de bout en bout —
// os/exec ne rend ErrWaitDelay que sur un statut de succès, et une annulation
// concomitante d'un succès est une course. Les exercer ici sur des valeurs
// fabriquées est la seule façon de ne pas les laisser sans preuve.
//
// Chaque condition, et ce qu'elle protège :
//   - le motif EST le drainage : un exit non nul, un exec.ErrNotFound ou une
//     erreur de copie restent des échecs de sbx et doivent remonter ;
//   - le contexte n'y est pour rien : une annulation ne doit jamais être
//     silencieusement convertie en succès ;
//   - le process est sorti avec un statut de SUCCÈS : c'est LUI qui atteste que
//     sbx a fait son travail, le reste n'étant que de la plomberie de tubes.
//
// Les deux dernières sont redondantes avec la première tant qu'os/exec tient son
// contrat documenté (ErrWaitDelay implique un statut de succès). Elles sont
// gardées parce que le prix d'un contrat qui changerait est ici de transformer
// un échec en succès — et cette redondance-là, on la paie volontiers.
func drainageEcourteSurSucces(err, errCtx error, etat *os.ProcessState) bool {
	return errors.Is(err, exec.ErrWaitDelay) &&
		errCtx == nil &&
		etat != nil && etat.Success()
}

// Run exécute sbx et renvoie stdout. En cas d'échec, stderr est INTÉGRÉ au
// message : sbx y met le diagnostic utile, et une erreur « exit status 1 » nue
// est inexploitable pour l'utilisateur comme pour le mainteneur. L'erreur
// d'origine (code de sortie, exec.ErrNotFound) et le motif d'annulation
// éventuel restent accessibles via errors.As/errors.Is grâce à
// ErreurExec.Unwrap — voir son commentaire.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Zéro ⇒ défaut, jamais « pas de borne » : la valeur nulle d'Exec (celle
	// que construisent les tests, `&Exec{Bin: "sh"}`) doit être la valeur SÛRE.
	// C'est toute la raison de delaiEffectif : passé tel quel à WaitDelay, 0
	// signifie précisément « attendre indéfiniment ».
	cmd.WaitDelay = delaiEffectif(e.DelaiDeDrainage)
	if err := cmd.Run(); err != nil {
		// Un drainage écourté sur un process qui a RÉUSSI n'est pas un échec de
		// sbx, et le rapporter comme tel a coûté une régression : `sbx create`
		// laissant derrière lui un superviseur qui hérite du tube faisait rendre
		// ErrWaitDelay À LA PLACE de nil — den annonçait « impossible de créer
		// la sandbox » alors qu'elle EXISTE, puis abandonnait la séquence, avec
		// en prime un message anglais interne à os/exec.
		//
		// Les trois conditions sont nécessaires : le seul motif est le drainage
		// (un exit non nul ou un ErrNotFound reste un échec), le contexte n'y est
		// pour rien (une annulation doit rester visible), et le process est sorti
		// avec un statut de SUCCÈS (c'est lui qui dit que sbx a fait son travail).
		//
		// La sortie collectée est complète, et pour une raison précise : le
		// copieur d'os/exec draine le tube EN CONTINU, donc tout ce que le fils
		// direct a écrit avant de sortir est déjà pris. Vérifié sur 240 Kio, très
		// au-delà du tampon d'un tube. Ce qu'un DESCENDANT écrirait après la
		// fermeture est perdu — c'est le comportement voulu, ce n'est pas la
		// sortie de sbx.
		if drainageEcourteSurSucces(err, ctx.Err(), cmd.ProcessState) {
			return stdout.Bytes(), nil
		}
		return stdout.Bytes(), &ErreurExec{
			Bin:    e.Bin,
			Args:   slices.Clone(args),
			Stderr: strings.TrimSpace(stderr.String()),
			Err:    err,
			// ctx.Err() est relevé ICI, au retour de cmd.Run : c'est le seul
			// endroit où l'on sait que l'échec et l'annulation sont concomitants.
			Annulation: ctx.Err(),
		}
	}
	return stdout.Bytes(), nil
}

// Attach donne la main à sbx sur les tty du processus courant.
func (e *Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	// Attach est la SEULE méthode de ce fichier où l'annulation du contexte ne
	// doit RIEN faire. Le comportement par défaut de CommandContext est un
	// SIGKILL du process (cmd.Cancel = Process.Kill) : sur un shell interactif,
	// ça laisse le terminal en mode raw, sans flush, sans `exit` propre. Un
	// Ctrl-C tapé DANS le shell de la sandbox est de toute façon délivré par le
	// driver tty au groupe de processus au premier plan, pas relayé via ce
	// contexte — den n'a donc rien à faire quand ce contexte se termine ici.
	//
	// cmd.Cancel = func() error { return nil } NE SUFFIT PAS : dans watchCtx
	// (os/exec), un Cancel qui renvoie nil sans être os.ErrProcessDone déclenche
	// quand même `err = ctx.Err()` après coup, même si le process se termine
	// ensuite avec succès (vérifié empiriquement, pas seulement lu dans la
	// doc). Renvoyer os.ErrProcessDone depuis Cancel obtiendrait le même effet
	// que ci-dessous ; Cancel = nil est retenu parce qu'il l'exprime plus
	// directement, pas parce que ce serait la seule forme qui marche.
	cmd.Cancel = nil
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// *ErreurExec et non un fmt.Errorf : le message rendu est le MÊME
		// (Stderr vide ⇒ ErreurExec.Error retombe sur e.Err, donc
		// « sbx exec -it … : exit status 1 », mot pour mot ce que produisait le
		// fmt.Errorf d'avant), mais le traitement du binaire absent est partagé
		// avec Run plutôt que dupliqué — et la chaîne d'erreurs gagne l'Unwrap
		// d'ErreurExec.
		//
		// Annulation reste NIL, délibérément : Attach est la seule méthode où
		// l'annulation du contexte ne doit rien faire (voir cmd.Cancel
		// ci-dessus), et la relever ici afficherait « interrompu » sur un shell
		// que den a justement laissé finir.
		return &ErreurExec{Bin: e.Bin, Args: slices.Clone(args), Err: err}
	}
	return nil
}
