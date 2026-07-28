package cli

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Tout den parle français, messages d'erreur compris. Cobra et pflag, eux,
// écrivent en anglais : gabarits d'aide, usage du flag --help, commandes
// `help` et `completion` générées, et messages d'analyse de flags. Ce fichier
// est le seul endroit où cette surface est traduite.
//
// CE QUI RESTE EN ANGLAIS, mesuré sur le binaire et non déduit du code :
//   - le corps de `den completion <shell> --help` (les instructions
//     d'installation, plusieurs paragraphes par shell) ; seule leur ligne de
//     description, celle qui s'affiche dans la liste, est traduite ;
//   - « Unknown help topic », écrit par le Run de la commande `help` générée ;
//   - le script de complétion lui-même (`den completion bash`), dont les
//     commentaires sont en anglais.
//
// Les traduire supposerait de réimplémenter ces commandes, donc d'en reprendre
// la maintenance : le compromis est assumé ici, il n'est pas un oubli.

// gabaritUsage est la traduction du defaultUsageTemplate de cobra. La LOGIQUE
// du gabarit est reprise à l'identique — mêmes conditions, mêmes champs, même
// disposition ; seuls les libellés changent.
//
// « [options] » plutôt que « [flags] » : cobra ajoute lui-même « [flags] » à la
// ligne d'usage, ce que DisableFlagsInUseLine (posé par franciseCobra sur tout
// l'arbre) désactive, la mention étant reprise ici en français.
const gabaritUsage = `Utilisation :{{if .Runnable}}
  {{.UseLine}}{{if .HasAvailableFlags}} [options]{{end}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [commande]{{end}}{{if gt (len .Aliases) 0}}

Alias :
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Exemples :
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Commandes disponibles :{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Autres commandes :{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Options :
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Options globales :
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Rubriques d'aide :{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Lancez « {{.CommandPath}} [commande] --help » pour l'aide d'une commande.{{end}}
`

// franciseCobra traduit la surface cobra de l'arbre passé. À appeler APRÈS que
// toutes les sous-commandes sont enregistrées : la francisation parcourt
// l'arbre, et ce qu'on y ajoute ensuite ne serait pas traité.
//
// Le gabarit et le FlagErrorFunc ne sont posés que sur la RACINE : cobra remonte
// l'arbre pour les trouver (UsageTemplate, FlagErrorFunc et consorts appellent
// c.parent quand le champ local est vide). C'est une propriété de cobra et non
// du projet, donc francais_test.go la vérifie sur trois niveaux plutôt que de la
// tenir pour acquise. L'usage du flag --help, lui, est bel et bien posé commande
// par commande : cobra en construit un par commande, sans aucun héritage.
func franciseCobra(root *cobra.Command) {
	root.SetUsageTemplate(gabaritUsage)
	root.SetFlagErrorFunc(traduitErreurDeFlag)

	// Les deux commandes que cobra ajoute tout seul. Les créer MAINTENANT est
	// ce qui permet de les traduire : cobra les ajoute sinon au moment de
	// l'exécution, trop tard pour ce parcours. Les deux méthodes sont
	// idempotentes (elles renoncent si la commande existe déjà), donc l'appel
	// que cobra fera à son tour ne les dupliquera pas.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	for _, cmd := range arbre(root) {
		cmd.DisableFlagsInUseLine = true
		// InitDefaultHelpFlag pose « help for <nom> » et ne fait rien si le flag
		// existe. On le pose donc nous-mêmes pour pouvoir en réécrire l'usage,
		// plutôt que de déclarer un --help concurrent qui perdrait l'annotation
		// que cobra y attache.
		cmd.InitDefaultHelpFlag()
		if f := cmd.Flags().Lookup("help"); f != nil {
			f.Usage = "affiche l'aide de " + cmd.Name()
		}
		switch {
		case cmd.Name() == "help":
			cmd.Short = "Affiche l'aide d'une commande"
			cmd.Long = "Affiche l'aide de n'importe quelle commande de " + root.Name() +
				".\nTapez « " + root.Name() + " help <commande> » pour le détail."
		case cmd.Name() == "completion":
			cmd.Short = "Génère le script de complétion d'un shell"
			cmd.Long = "Génère le script de complétion de " + root.Name() +
				" pour le shell demandé.\nVoir l'aide de chaque sous-commande pour l'installer."
		// Les commandes par shell (bash, zsh, fish, powershell) sont générées
		// par cobra sous `completion`. Le test porte sur le PARENT et non sur
		// une liste de noms de shells : cobra peut en ajouter un, et une liste
		// en dur laisserait le nouveau en anglais sans que rien ne le dise.
		case cmd.Parent() != nil && cmd.Parent().Name() == "completion":
			cmd.Short = "Génère le script de complétion pour " + cmd.Name()
		}
	}
}

// arbre rend la commande et toute sa descendance, racine comprise.
func arbre(cmd *cobra.Command) []*cobra.Command {
	tout := []*cobra.Command{cmd}
	for _, enfant := range cmd.Commands() {
		tout = append(tout, arbre(enfant)...)
	}
	return tout
}

// traduitErreurDeFlag rend en français les erreurs d'analyse de pflag.
//
// La traduction s'appuie sur les TYPES d'erreur exportés par pflag et sur leurs
// accesseurs, jamais sur le texte anglais : un message reformulé en amont ferait
// silencieusement retomber une comparaison de chaînes dans le cas « inconnu »,
// et l'anglais reviendrait sans que rien ne le signale.
//
// Ce qui n'est pas reconnu remonte TEL QUEL. Un message anglais exact vaut mieux
// qu'un message français faux, et c'est vérifié par un test.
func traduitErreurDeFlag(_ *cobra.Command, err error) error {
	var inexistant *pflag.NotExistError
	if errors.As(err, &inexistant) {
		// GetSpecifiedShortnames n'est renseigné que pour un groupe de flags
		// courts (« -api ») : c'est ce qui distingue les deux formes, le type
		// étant commun aux deux et le discriminant de pflag non exporté.
		if groupe := inexistant.GetSpecifiedShortnames(); groupe != "" {
			// pflag ne rapporte que la PREMIÈRE lettre fautive du groupe, et
			// GetSpecifiedName rend tout le reste du groupe : « -api » donne
			// « api », dont seul « a » est en cause.
			premiere, _ := utf8.DecodeRuneInString(inexistant.GetSpecifiedName())
			return fmt.Errorf("option courte inconnue : « %c » dans -%s", premiere, groupe)
		}
		return fmt.Errorf("option inconnue : --%s", inexistant.GetSpecifiedName())
	}

	var valeurRequise *pflag.ValueRequiredError
	if errors.As(err, &valeurRequise) {
		if groupe := valeurRequise.GetSpecifiedShortnames(); groupe != "" {
			return fmt.Errorf("valeur manquante pour l'option courte « %s » dans -%s",
				valeurRequise.GetSpecifiedName(), groupe)
		}
		return fmt.Errorf("valeur manquante pour l'option --%s", valeurRequise.GetSpecifiedName())
	}

	var valeurInvalide *pflag.InvalidValueError
	if errors.As(err, &valeurInvalide) {
		return fmt.Errorf("valeur invalide « %s » pour l'option --%s : %w",
			valeurInvalide.GetValue(), valeurInvalide.GetFlag().Name, valeurInvalide.Unwrap())
	}

	var syntaxe *pflag.InvalidSyntaxError
	if errors.As(err, &syntaxe) {
		return fmt.Errorf("syntaxe d'option invalide : %s", syntaxe.GetSpecifiedFlag())
	}

	return err
}
