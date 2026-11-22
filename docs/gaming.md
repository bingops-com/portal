# Serveurs de jeu par Git

Le widget `gamerooms` pilote les serveurs existants autorisés dans la
configuration du backend. Démarrer ou mettre en veille écrit un commit ; un
changement de version crée une pull request. Aucune IA n'intervient dans ces
opérations. L'ajout de nouveaux GameRooms se fait encore dans les manifests Git.
La création par formulaire et les autres jeux ne font pas partie de ce MVP.

Le navigateur voit un diff avant confirmation. Un commit accepté signifie
« demande enregistrée ». L'interface attend ensuite la révision Argo CD, les
champs souhaités du GameRoom et sa génération observée pour présenter l'état
réel comme courant. Une PR de version ne modifie pas le serveur avant fusion.
Un ancien état Kubernetes n'est jamais présenté comme la réussite du dernier
clic. Une observation indisponible reste explicitement signalée.

## Configuration

1. Installer le contrôleur GameRoom et son catalogue de releases immuables.
   Faire suivre uniquement les manifests GameRoom par une Application Argo CD.
2. Fournir `PORTAL_GAMES_CONFIG`, chemin d'un fichier JSON monté en lecture
   seule. `config/gaming.example.json` décrit les champs publics. Dépôt,
   branche, répertoire, namespace, passerelle et liste des serveurs autorisés
   sont déterminés par ce fichier, jamais par une requête du navigateur.
3. Conserver l'OIDC du portail et définir explicitement `PORTAL_OIDC_GROUP`
   pour le propriétaire. Le programme refuse d'activer cette fonction sans
   OIDC et groupe. Les lectures et commandes de jeu exigent une session,
   même lorsque les autres pages du portail sont ouvertes sur le LAN.
4. Accorder au compte Kubernetes du portail `get` sur les GameRooms autorisés
   dans leur namespace et sur l'Application Argo CD. Aucun droit d'écriture,
   aucune lecture de Secrets ou ConfigMaps n'est nécessaire.
5. Ajouter un widget `{ type: gamerooms, title: Serveurs de jeu }` dans une
   colonne pleine largeur du YAML versionné, ou depuis l'éditeur existant.

Le catalogue est lu dans Git à la même révision que les manifests : ConfigMap
`gaas-release-catalog`, champ `releases.json`. Seules les releases comprenant
un digest d'image, un build Steam et un snapshot binaire complet sont proposées.
Les volumes, dépôts de sauvegarde, secrets et ports ne sont pas éditables dans
ce widget. Le backend refuse les fichiers contenant plusieurs documents,
des alias YAML ou des clés dupliquées et vérifie le périmètre du GameRoom.

Si `stateWorkflow` nomme un workflow YAML, le mode sans App propose un lien
vers son formulaire GitHub pour démarrer/mettre en veille. Le propriétaire
s’authentifie sur GitHub ; le workflow valide le périmètre et écrit son commit
avec son jeton de dépôt. Le portail suit ensuite l’état via Argo/Kubernetes.
Aucun jeton GitHub supplémentaire n’est nécessaire pour ce parcours. L’App
reste nécessaire pour les boutons intégrés et les PR de version du portail.

## GitHub App et secrets

Sans GitHub App, le backend peut lire un dépôt public mais les commandes sont
désactivées. Pour écrire, le propriétaire GitHub doit enregistrer une App
privée dédiée, désactiver son webhook et lui accorder **Contents: Read & write**
et **Pull requests: Read & write**, puis l'installer uniquement sur le dépôt
des manifests. Il ne faut pas réutiliser les droits du connecteur Codex ou
étendre ceux de l'App des runners ARC pour cette fonction.

Stocker la clé privée PEM dans Bitwarden Secrets Manager. Git conserve son
mapping Bitwarden, les identifiants publics de l'App/installation et les refs
Kubernetes, jamais la clé. Monter le Secret matérialisé en lecture seule :

| Entrée du portail | Origine |
| --- | --- |
| `PORTAL_GAMES_GITHUB_APP_ID` | Identifiant public de l'App, configuration Git |
| `PORTAL_GAMES_GITHUB_INSTALLATION_ID` | Identifiant public de l'installation |
| `PORTAL_GAMES_GITHUB_KEY_FILE` | Chemin du PEM fourni par un volume Secret |

Le backend signe un JWT et obtient un jeton d'installation limité au dépôt et
aux deux permissions ci-dessus. Il le renouvelle en mémoire avant expiration ;
le navigateur ne reçoit aucun jeton. Les réponses d'erreur GitHub brutes ne
sont pas exposées. Les permissions GitHub restent à l'échelle du dépôt : le
contrôle des chemins est assuré par le backend. Un dépôt dédié isole davantage
ce pouvoir qu'une installation sur l'ensemble de LabOps.

La branche doit autoriser les commits d'état de l'App selon ses règles. Aucun
contournement de protection ni force push n'est utilisé. La mise à jour de ref
est refusée si la branche a avancé ; l'utilisateur doit alors revoir un diff
calculé sur la nouvelle révision. Les reprises réseau réutilisent l'identifiant
de commande, pour retrouver le commit ou la PR déjà créé. L'identité OIDC du
demandeur figure dans le message de commit et la PR.

## Activation et reconstruction

L'enregistrement/installation de l'App et l'entrée Bitwarden sont les seuls
prérequis externes nouveaux. Leur propriétaire est l'administrateur GitHub de
l'organisation. Pour reconstruire : récupérer cette entrée Bitwarden, rétablir
le mapping et le montage Secret, la configuration publique, le RBAC de lecture
et l'OIDC, puis déployer l'image versionnée. Le PVC du portail conserve la clé
de session OIDC existante, mais ne sert pas de coffre pour la clé GitHub.

Pour faire tourner la clé, générer une nouvelle clé dans les réglages de l'App,
remplacer la valeur Bitwarden, relancer le portail et vérifier une opération
sur un GameRoom de test avant de révoquer l'ancienne clé. Une App supprimée ou
désinstallée doit être recréée/réinstallée et ses identifiants publics mis à
jour dans Git. Vérifier les identifiants, les droits, l'accès au dépôt et un
commit de test sans afficher la clé ni un jeton.

Ce code est préparé sur `feat/game-room-portal`. Il n'a pas remplacé l'image du
portail en production et aucune GitHub App dédiée n'a été installée par cette
intervention. Les tests Go utilisent une API GitHub locale ; les tests navigateur
vérifient le parcours client avec des réponses API simulées. Ces tests ne prouvent
pas encore que l'installation réelle de l'App dispose des droits attendus.

Références : [jetons d'installation GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app),
[mise à jour des références Git](https://docs.github.com/en/rest/git/refs#update-a-reference).
