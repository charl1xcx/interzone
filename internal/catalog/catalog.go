// Package catalog describes everything matrix-docker-ansible-deploy (MDAD)
// can install: homeserver implementations and optional components, together
// with the vars.yml variables each one needs.
package catalog

// Kind controls how a field is edited in the UI and rendered into vars.yml.
type Kind int

const (
	Text   Kind = iota // quoted YAML string
	Secret             // quoted string, masked in the UI, auto-generated when empty
	Hex64              // 64 hex characters, auto-generated when empty
	Number             // raw scalar
	Bool               // true/false
	List               // comma separated in the UI, YAML list in vars.yml
	YAML               // raw YAML block nested under the variable
)

type Field struct {
	Var      string
	Label    string
	Help     string
	Kind     Kind
	Default  string
	Required bool
	// ShowIf names a Bool field of the same component that must be on for
	// this field to be shown and written.
	ShowIf string
}

type Component struct {
	ID          string
	Name        string
	Category    string
	Description string
	EnableVar   string
	// DefaultOn components are enabled by MDAD unless explicitly disabled,
	// so their enable variable is always written out.
	DefaultOn bool
	// StartOff keeps a DefaultOn component unticked in a new setup.
	StartOff bool
	// Suggested components are pointed out on the Components step.
	Suggested bool
	// Extra variables written verbatim when the component is enabled.
	Extra       map[string]string
	Fields      []Field
	SynapseOnly bool
	Status      string // "", "unmaintained" or "deprecated"
	Doc         string
	// Subdomain is the DNS label the component needs ("" if it lives on matrix.<domain>).
	Subdomain string
}

func (c Component) DocURL(base string) string {
	return base + "/blob/master/docs/" + c.Doc
}

type Homeserver struct {
	ID          string
	Name        string
	Tagline     string
	Description string
}

var Homeservers = []Homeserver{
	{"synapse", "Synapse", "the reference server · recommended",
		"Most complete and best supported implementation. Every component in this tool works with it."},
	{"continuwuity", "Continuwuity", "lightweight Rust server",
		"Community fork of conduwuit. Low memory use; some Synapse-only extras are hidden."},
	{"tuwunel", "Tuwunel", "lightweight Rust server",
		"Enterprise-backed successor of conduwuit. Low memory use; some Synapse-only extras are hidden."},
	{"conduit", "Conduit", "minimal Rust server",
		"The original Conduit. Very small footprint, fewer features."},
	{"dendrite", "Dendrite", "second-gen Go server",
		"In maintenance mode upstream. Only pick this if you know you need it."},
}

var Categories = []string{
	"Core services",
	"Clients",
	"Authentication",
	"File storage",
	"Bridges",
	"Bots",
	"Administration",
	"Calls & extras",
}

// Components lists every component MDAD can still install. Removed ones are left out.
var Components = []Component{
	// ── Core services ──────────────────────────────────────────────
	{ID: "coturn", Name: "Coturn TURN server", Category: "Core services", EnableVar: "coturn_enabled", DefaultOn: true,
		Description: "Relays voice/video calls for users behind NAT. Strongly recommended.", Doc: "configuring-playbook-turn.md",
		Fields: []Field{{Var: "coturn_turn_external_ip_addresses", Label: "Public IP address(es)", Help: "Leave empty to auto-detect (auto-detection needs internet).", Kind: List}}},
	{ID: "exim", Name: "Exim email relay", Category: "Core services", EnableVar: "exim_relay_enabled", DefaultOn: true, StartOff: true,
		Description: "Lets services send email (password resets, notifications).", Doc: "configuring-playbook-email.md",
		Fields: []Field{
			{Var: "exim_relay_sender_address", Label: "Sender address", Help: "Leave empty for matrix@matrix.<domain>.", Kind: Text},
			{Var: "exim_relay_relay_use", Label: "Relay through another SMTP server", Kind: Bool, Default: "false"},
			{Var: "exim_relay_relay_host_name", Label: "SMTP host", Kind: Text, ShowIf: "exim_relay_relay_use"},
			{Var: "exim_relay_relay_host_port", Label: "SMTP port", Kind: Number, Default: "587", ShowIf: "exim_relay_relay_use"},
			{Var: "exim_relay_relay_auth", Label: "SMTP needs authentication", Kind: Bool, Default: "false", ShowIf: "exim_relay_relay_use"},
			{Var: "exim_relay_relay_auth_username", Label: "SMTP username", Kind: Text, ShowIf: "exim_relay_relay_auth"},
			{Var: "exim_relay_relay_auth_password", Label: "SMTP password", Kind: Secret, ShowIf: "exim_relay_relay_auth"},
		}},
	{ID: "base-domain", Name: "Serve the base domain", Category: "Core services", EnableVar: "matrix_static_files_container_labels_base_domain_enabled",
		Description: "Host a simple page and .well-known files on <domain> itself (only if nothing else runs there).", Doc: "configuring-playbook-base-domain-serving.md"},
	{ID: "ddclient", Name: "Dynamic DNS (ddclient)", Category: "Core services", EnableVar: "ddclient_enabled",
		Description: "Keep DNS records updated when your server has a changing IP.", Doc: "configuring-playbook-dynamic-dns.md",
		Fields: []Field{{Var: "ddclient_domain_configurations", Label: "Provider configuration", Kind: YAML, Required: true,
			Default: "- provider: example.net\n  protocol: dyndns2\n  username: YOUR_USERNAME_HERE\n  password: YOUR_PASSWORD_HERE\n  domain: \"{{ matrix_domain }}\""}}},
	{ID: "external-postgres", Name: "Use external PostgreSQL", Category: "Core services", EnableVar: "",
		Description: "Use your own Postgres server instead of the bundled one.", Doc: "configuring-playbook-external-postgres.md",
		Extra: map[string]string{"postgres_enabled": "false"},
		Fields: []Field{
			{Var: "matrix_synapse_database_host", Label: "Database host", Kind: Text, Required: true},
			{Var: "matrix_synapse_database_user", Label: "Database user", Kind: Text, Required: true},
			{Var: "matrix_synapse_database_password", Label: "Database password", Kind: Text, Required: true},
			{Var: "matrix_synapse_database_database", Label: "Database name", Kind: Text, Default: "synapse"},
		}},
	{ID: "telemetry", Name: "Report anonymous usage stats", Category: "Core services", EnableVar: "matrix_synapse_report_stats",
		Description: "Send anonymous statistics to matrix.org (needs internet).", Doc: "configuring-playbook-telemetry.md", SynapseOnly: true},

	// ── Clients ────────────────────────────────────────────────────
	{ID: "element", Name: "Element Web", Category: "Clients", EnableVar: "matrix_client_element_enabled", DefaultOn: true, Subdomain: "element",
		Description: "The flagship Matrix web client.", Doc: "configuring-playbook-client-element-web.md"},
	{ID: "cinny", Name: "Cinny", Category: "Clients", EnableVar: "cinny_enabled", Subdomain: "cinny",
		Description: "A simple, elegant web client.", Doc: "configuring-playbook-client-cinny.md"},
	{ID: "sable", Name: "Sable", Category: "Clients", EnableVar: "sable_enabled", Subdomain: "sable", Suggested: true,
		Description: "Web client focused on a clean, elegant interface.", Doc: "configuring-playbook-client-sable.md"},
	{ID: "hydrogen", Name: "Hydrogen", Category: "Clients", EnableVar: "hydrogen_enabled", Subdomain: "hydrogen",
		Description: "Lightweight client with legacy and mobile browser support.", Doc: "configuring-playbook-client-hydrogen.md"},
	{ID: "schildichat", Name: "SchildiChat Web", Category: "Clients", EnableVar: "matrix_client_schildichat_enabled", Subdomain: "schildichat",
		Description: "Element-based client with extras and tweaks.", Doc: "configuring-playbook-client-schildichat-web.md"},
	{ID: "fluffychat", Name: "FluffyChat Web", Category: "Clients", EnableVar: "matrix_client_fluffychat_enabled", Subdomain: "fluffychat",
		Description: "A cute cross-platform messenger written in Flutter.", Doc: "configuring-playbook-client-fluffychat-web.md"},
	{ID: "commet", Name: "Commet", Category: "Clients", EnableVar: "matrix_client_commet_enabled", Subdomain: "commet",
		Description: "A feature-rich Matrix web client.", Doc: "configuring-playbook-client-commet.md"},

	// ── Authentication ─────────────────────────────────────────────
	{ID: "mas", Name: "Matrix Authentication Service", Category: "Authentication", EnableVar: "matrix_authentication_service_enabled", SynapseOnly: true,
		Description: "OAuth 2.0/OIDC logins, which Element X needs. On by default with Synapse; untick it to keep Synapse's own logins.", Doc: "configuring-playbook-matrix-authentication-service.md"},
	{ID: "double-puppet", Name: "Appservice Double Puppet", Category: "Authentication", EnableVar: "matrix_appservice_double_puppet_enabled",
		Description: "Lets bridges show your messages sent from other apps as coming from you.", Doc: "configuring-playbook-appservice-double-puppet.md"},
	{ID: "ketesa", Name: "Ketesa admin panel", Category: "Authentication", EnableVar: "matrix_ketesa_enabled", SynapseOnly: true,
		Description: "Web UI for managing users, rooms and media (synapse-admin successor).", Doc: "configuring-playbook-ketesa.md"},
	{ID: "element-admin", Name: "Element Admin", Category: "Authentication", EnableVar: "matrix_element_admin_enabled",
		Description: "Element's admin web app. Works best together with MAS.", Doc: "configuring-playbook-element-admin.md", Subdomain: "admin.element"},
	{ID: "rest-auth", Name: "REST auth password provider", Category: "Authentication", EnableVar: "matrix_synapse_ext_password_provider_rest_auth_enabled", SynapseOnly: true,
		Description: "Authenticate users against your own REST endpoint.", Doc: "configuring-playbook-rest-auth.md",
		Fields: []Field{{Var: "matrix_synapse_ext_password_provider_rest_auth_endpoint", Label: "Endpoint URL", Kind: Text, Required: true}}},
	{ID: "shared-secret-auth", Name: "Shared Secret Auth", Category: "Authentication", EnableVar: "matrix_synapse_ext_password_provider_shared_secret_auth_enabled", SynapseOnly: true,
		Description: "Log in as any user with a shared secret (used by bots and integrations).", Doc: "configuring-playbook-shared-secret-auth.md",
		Fields: []Field{{Var: "matrix_synapse_ext_password_provider_shared_secret_auth_shared_secret", Label: "Shared secret", Kind: Secret, Required: true}}},
	{ID: "ldap-auth", Name: "LDAP auth password provider", Category: "Authentication", EnableVar: "matrix_synapse_ext_password_provider_ldap_enabled", SynapseOnly: true,
		Description: "Log in with accounts from an LDAP directory.", Doc: "configuring-playbook-ldap-auth.md",
		Fields: []Field{
			{Var: "matrix_synapse_ext_password_provider_ldap_uri", Label: "LDAP URIs", Kind: List, Required: true, Help: "e.g. ldap://ldap.example.com:389"},
			{Var: "matrix_synapse_ext_password_provider_ldap_start_tls", Label: "Use StartTLS", Kind: Bool, Default: "true"},
			{Var: "matrix_synapse_ext_password_provider_ldap_base", Label: "Base DN", Kind: Text, Required: true},
			{Var: "matrix_synapse_ext_password_provider_ldap_attributes_uid", Label: "UID attribute", Kind: Text, Default: "uid"},
			{Var: "matrix_synapse_ext_password_provider_ldap_attributes_mail", Label: "Mail attribute", Kind: Text, Default: "mail"},
			{Var: "matrix_synapse_ext_password_provider_ldap_attributes_name", Label: "Name attribute", Kind: Text, Default: "cn"},
			{Var: "matrix_synapse_ext_password_provider_ldap_bind_dn", Label: "Bind DN", Kind: Text},
			{Var: "matrix_synapse_ext_password_provider_ldap_bind_password", Label: "Bind password", Kind: Text},
		}},
	{ID: "simple-antispam", Name: "Synapse Simple Antispam", Category: "Authentication", EnableVar: "matrix_synapse_ext_spam_checker_synapse_simple_antispam_enabled", SynapseOnly: true,
		Description: "Block invites from specific homeservers.", Doc: "configuring-playbook-synapse-simple-antispam.md",
		Fields: []Field{{Var: "matrix_synapse_ext_spam_checker_synapse_simple_antispam_config_blocked_homeservers", Label: "Blocked homeservers", Kind: List, Required: true}}},
	{ID: "uvs", Name: "User Verification Service", Category: "Authentication", EnableVar: "matrix_user_verification_service_enabled",
		Description: "Verify users' room membership for Jitsi and other widgets.", Doc: "configuring-playbook-user-verification-service.md",
		Fields: []Field{
			{Var: "matrix_user_verification_service_uvs_access_token", Label: "Access token of a bot user", Kind: Text, Required: true},
			{Var: "matrix_user_verification_service_uvs_auth_token", Label: "Auth token", Kind: Secret},
		}},

	// ── File storage ───────────────────────────────────────────────
	{ID: "media-repo", Name: "matrix-media-repo", Category: "File storage", EnableVar: "matrix_media_repo_enabled",
		Description: "Advanced media repository with deduplication and multiple backends.", Doc: "configuring-playbook-matrix-media-repo.md",
		Fields: []Field{{Var: "matrix_media_repo_datastore_file_id", Label: "Datastore ID", Kind: Secret}}},
	{ID: "s3-provider", Name: "S3 storage provider", Category: "File storage", EnableVar: "matrix_synapse_ext_synapse_s3_storage_provider_enabled", SynapseOnly: true,
		Description: "Offload Synapse media to S3-compatible object storage.", Doc: "configuring-playbook-synapse-s3-storage-provider.md",
		Fields: []Field{
			{Var: "matrix_synapse_ext_synapse_s3_storage_provider_config_bucket", Label: "Bucket", Kind: Text, Required: true},
			{Var: "matrix_synapse_ext_synapse_s3_storage_provider_config_region_name", Label: "Region", Kind: Text, Required: true},
			{Var: "matrix_synapse_ext_synapse_s3_storage_provider_config_endpoint_url", Label: "Endpoint URL", Kind: Text, Required: true},
			{Var: "matrix_synapse_ext_synapse_s3_storage_provider_config_access_key_id", Label: "Access key ID", Kind: Text, Required: true},
			{Var: "matrix_synapse_ext_synapse_s3_storage_provider_config_secret_access_key", Label: "Secret access key", Kind: Text, Required: true},
		}},
	{ID: "goofys", Name: "S3 via Goofys", Category: "File storage", EnableVar: "matrix_s3_media_store_enabled", SynapseOnly: true,
		Description: "Mount an S3 bucket as Synapse's media store.", Doc: "configuring-playbook-s3-goofys.md",
		Fields: []Field{
			{Var: "matrix_s3_media_store_bucket_name", Label: "Bucket", Kind: Text, Required: true},
			{Var: "matrix_s3_media_store_aws_access_key", Label: "Access key", Kind: Text, Required: true},
			{Var: "matrix_s3_media_store_aws_secret_key", Label: "Secret key", Kind: Text, Required: true},
			{Var: "matrix_s3_media_store_region", Label: "Region", Kind: Text, Default: "eu-central-1"},
			{Var: "matrix_s3_media_store_custom_endpoint_enabled", Label: "Custom endpoint", Kind: Bool, Default: "false"},
			{Var: "matrix_s3_media_store_custom_endpoint", Label: "Endpoint URL", Kind: Text, ShowIf: "matrix_s3_media_store_custom_endpoint_enabled"},
		}},

	// ── Bridges ────────────────────────────────────────────────────
	{ID: "bridges-e2ee", Name: "Bridge encryption (all bridges)", Category: "Bridges", EnableVar: "matrix_bridges_encryption_enabled",
		Description: "Enable end-to-bridge encryption for every mautrix bridge.", Doc: "configuring-playbook-bridge-mautrix-bridges.md"},
	{ID: "bridges-relay", Name: "Bridge relay mode (all bridges)", Category: "Bridges", EnableVar: "matrix_bridges_relay_enabled",
		Description: "Let bridged rooms relay messages from users who haven't logged into the bridge.", Doc: "configuring-playbook-bridge-mautrix-bridges.md"},
	{ID: "telegram", Name: "Telegram (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_telegram_enabled",
		Description: "Bridge Telegram chats. Needs an API ID/hash from my.telegram.org.", Doc: "configuring-playbook-bridge-mautrix-telegram.md",
		Fields: []Field{
			{Var: "matrix_bridge_mautrix_telegram_api_id", Label: "Telegram API ID", Kind: Number, Required: true},
			{Var: "matrix_bridge_mautrix_telegram_api_hash", Label: "Telegram API hash", Kind: Text, Required: true},
		}},
	{ID: "whatsapp", Name: "WhatsApp (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_whatsapp_enabled",
		Description: "Bridge WhatsApp chats via the multidevice API.", Doc: "configuring-playbook-bridge-mautrix-whatsapp.md"},
	{ID: "signal", Name: "Signal (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_signal_enabled",
		Description: "Bridge Signal chats as a linked device.", Doc: "configuring-playbook-bridge-mautrix-signal.md"},
	{ID: "discord", Name: "Discord (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_discord_enabled",
		Description: "Puppeting Discord bridge.", Doc: "configuring-playbook-bridge-mautrix-discord.md"},
	{ID: "slack", Name: "Slack (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_slack_enabled",
		Description: "Puppeting Slack bridge.", Doc: "configuring-playbook-bridge-mautrix-slack.md"},
	{ID: "meta-messenger", Name: "Messenger (mautrix-meta)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_meta_messenger_enabled",
		Description: "Bridge Facebook Messenger.", Doc: "configuring-playbook-bridge-mautrix-meta-messenger.md"},
	{ID: "meta-instagram", Name: "Instagram (mautrix-meta)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_meta_instagram_enabled",
		Description: "Bridge Instagram DMs.", Doc: "configuring-playbook-bridge-mautrix-meta-instagram.md"},
	{ID: "bluesky", Name: "Bluesky (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_bluesky_enabled",
		Description: "Bridge Bluesky direct messages.", Doc: "configuring-playbook-bridge-mautrix-bluesky.md"},
	{ID: "twitter", Name: "Twitter/X (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_twitter_enabled",
		Description: "Bridge Twitter/X direct messages.", Doc: "configuring-playbook-bridge-mautrix-twitter.md"},
	{ID: "linkedin", Name: "LinkedIn (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_linkedin_enabled",
		Description: "Bridge LinkedIn messaging.", Doc: "configuring-playbook-bridge-mautrix-linkedin.md"},
	{ID: "googlechat", Name: "Google Chat (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_googlechat_enabled",
		Description: "Bridge Google Chat.", Doc: "configuring-playbook-bridge-mautrix-googlechat.md"},
	{ID: "gmessages", Name: "Google Messages (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_gmessages_enabled",
		Description: "Bridge SMS/RCS through Google Messages on your phone.", Doc: "configuring-playbook-bridge-mautrix-gmessages.md"},
	{ID: "gvoice", Name: "Google Voice (mautrix)", Category: "Bridges", EnableVar: "matrix_bridge_mautrix_gvoice_enabled",
		Description: "Bridge Google Voice.", Doc: "configuring-playbook-bridge-mautrix-gvoice.md"},
	{ID: "rustpush", Name: "iMessage (RustPush)", Category: "Bridges", EnableVar: "matrix_bridge_rustpush_enabled",
		Description: "Bridge Apple iMessage.", Doc: "configuring-playbook-bridge-rustpush.md"},
	{ID: "appservice-irc", Name: "IRC (matrix-appservice-irc)", Category: "Bridges", EnableVar: "matrix_bridge_appservice_irc_enabled",
		Description: "Bridge one or more IRC networks.", Doc: "configuring-playbook-bridge-appservice-irc.md",
		Fields: []Field{{Var: "matrix_bridge_appservice_irc_ircService_servers", Label: "IRC servers", Kind: YAML, Required: true,
			Default: "irc.example.com:\n  name: \"ExampleNet\"\n  port: 6697\n  ssl: true\n  botConfig:\n    enabled: true\n    nick: \"MatrixBot\"\n  dynamicChannels:\n    enabled: true\n    aliasTemplate: \"#irc_$CHANNEL\"\n  matrixClients:\n    userTemplate: \"@irc_$NICK\"\n  ircClients:\n    nickTemplate: \"$DISPLAY[m]\""}}},
	{ID: "heisenbridge", Name: "Heisenbridge (IRC bouncer)", Category: "Bridges", EnableVar: "matrix_bridge_heisenbridge_enabled",
		Description: "Bouncer-style IRC bridge, one connection per user.", Doc: "configuring-playbook-bridge-heisenbridge.md"},
	{ID: "appservice-discord", Name: "Discord (matrix-appservice-discord)", Category: "Bridges", EnableVar: "matrix_bridge_appservice_discord_enabled",
		Description: "Bot-based (non-puppeting) Discord bridge.", Doc: "configuring-playbook-bridge-appservice-discord.md",
		Fields: []Field{
			{Var: "matrix_bridge_appservice_discord_client_id", Label: "Discord app client ID", Kind: Text, Required: true},
			{Var: "matrix_bridge_appservice_discord_bot_token", Label: "Discord bot token", Kind: Text, Required: true},
		}},
	{ID: "hookshot", Name: "matrix-hookshot", Category: "Bridges", EnableVar: "matrix_bridge_hookshot_enabled",
		Description: "Webhooks plus GitHub, GitLab, JIRA and RSS feeds.", Doc: "configuring-playbook-bridge-hookshot.md"},
	{ID: "beeper-line", Name: "LINE (Beeper)", Category: "Bridges", EnableVar: "matrix_bridge_beeper_line_enabled",
		Description: "Bridge LINE messenger.", Doc: "configuring-playbook-bridge-beeper-line.md"},
	{ID: "beeper-linkedin", Name: "LinkedIn (Beeper)", Category: "Bridges", EnableVar: "matrix_bridge_beeper_linkedin_enabled", Status: "unmaintained",
		Description: "Archived upstream. Prefer the mautrix LinkedIn bridge.", Doc: "configuring-playbook-bridge-beeper-linkedin.md"},
	{ID: "groupme", Name: "GroupMe (mx-puppet)", Category: "Bridges", EnableVar: "matrix_bridge_mx_puppet_groupme_enabled",
		Description: "Bridge GroupMe.", Doc: "configuring-playbook-bridge-mx-puppet-groupme.md"},
	{ID: "steam", Name: "Steam", Category: "Bridges", EnableVar: "matrix_bridge_steam_enabled",
		Description: "Bridge Steam chat.", Doc: "configuring-playbook-bridge-steam.md"},
	{ID: "mx-puppet-steam", Name: "Steam (mx-puppet)", Category: "Bridges", EnableVar: "matrix_bridge_mx_puppet_steam_enabled",
		Description: "Alternative Steam bridge.", Doc: "configuring-playbook-bridge-mx-puppet-steam.md"},
	{ID: "postmoogle", Name: "Postmoogle (email)", Category: "Bridges", EnableVar: "matrix_bridge_postmoogle_enabled",
		Description: "Send and receive email in Matrix rooms.", Doc: "configuring-playbook-bridge-postmoogle.md",
		Fields: []Field{{Var: "matrix_bridge_postmoogle_password", Label: "Bot password", Kind: Secret, Required: true}}},
	{ID: "sms", Name: "SMS (matrix-sms-bridge)", Category: "Bridges", EnableVar: "matrix_bridge_sms_enabled",
		Description: "Bridge SMS through an Android SMS gateway.", Doc: "configuring-playbook-bridge-matrix-bridge-sms.md",
		Fields: []Field{
			{Var: "matrix_bridge_sms_default_region", Label: "Default region (e.g. DE)", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_default_timezone", Label: "Timezone (e.g. Europe/Berlin)", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_provider_android_baseurl", Label: "Android gateway URL", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_provider_android_username", Label: "Gateway username", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_provider_android_password", Label: "Gateway password", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_provider_android_truststore_local_path", Label: "Truststore file name", Kind: Text, Required: true},
			{Var: "matrix_bridge_sms_provider_android_truststore_password", Label: "Truststore password", Kind: Text, Required: true},
		}},
	{ID: "meshtastic", Name: "Meshtastic relay", Category: "Bridges", EnableVar: "matrix_bridge_meshtastic_relay_enabled",
		Description: "Bridge Meshtastic LoRa mesh radios. Useful when the internet is down!", Doc: "configuring-playbook-bridge-meshtastic-relay.md",
		Fields: []Field{
			{Var: "matrix_bridge_meshtastic_relay_matrix_bot_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bridge_meshtastic_relay_connection_type", Label: "Connection type (tcp/serial/ble)", Kind: Text, Default: "tcp"},
			{Var: "matrix_bridge_meshtastic_relay_tcp_host", Label: "Meshtastic node host", Kind: Text, Default: "meshtastic.local"},
			{Var: "matrix_bridge_meshtastic_relay_matrix_rooms_list", Label: "Rooms", Kind: YAML,
				Default: "- id: \"#meshtastic:{{ matrix_domain }}\"\n  meshtastic_channel: \"0\""},
		}},
	{ID: "wechat", Name: "WeChat", Category: "Bridges", EnableVar: "matrix_bridge_wechat_enabled",
		Description: "Bridge WeChat.", Doc: "configuring-playbook-bridge-wechat.md"},

	// ── Bots ───────────────────────────────────────────────────────
	{ID: "baibot", Name: "baibot (AI bot)", Category: "Bots", EnableVar: "matrix_bot_baibot_enabled",
		Description: "Chat with LLMs (OpenAI, Anthropic, Venice, local models...).", Doc: "configuring-playbook-bot-baibot.md",
		Fields: []Field{
			{Var: "matrix_bot_baibot_config_user_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bot_baibot_config_user_encryption_recovery_passphrase", Label: "Recovery passphrase", Kind: Secret, Required: true},
			{Var: "matrix_bot_baibot_config_persistence_session_encryption_key", Label: "Session encryption key", Kind: Hex64, Required: true},
			{Var: "matrix_bot_baibot_config_persistence_config_encryption_key", Label: "Config encryption key", Kind: Hex64, Required: true},
		}},
	{ID: "reminder-bot", Name: "matrix-reminder-bot", Category: "Bots", EnableVar: "matrix_bot_matrix_reminder_bot_enabled",
		Description: "A bot to remind you about stuff.", Doc: "configuring-playbook-bot-matrix-reminder-bot.md",
		Fields: []Field{
			{Var: "matrix_bot_matrix_reminder_bot_matrix_user_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bot_matrix_reminder_bot_reminders_timezone", Label: "Timezone", Kind: Text, Default: "Asia/Tehran", Required: true},
		}},
	{ID: "maubot", Name: "maubot", Category: "Bots", EnableVar: "matrix_bot_maubot_enabled",
		Description: "Plugin-based bot system.", Doc: "configuring-playbook-bot-maubot.md",
		Fields: []Field{
			{Var: "matrix_bot_maubot_initial_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bot_maubot_admins", Label: "Management UI admins", Kind: YAML, Default: "- admin: CHANGE_ME"},
		}},
	{ID: "honoroit", Name: "Honoroit (helpdesk)", Category: "Bots", EnableVar: "matrix_bot_honoroit_enabled",
		Description: "Helpdesk bot that routes users' questions to a team room.", Doc: "configuring-playbook-bot-honoroit.md",
		Fields: []Field{
			{Var: "matrix_bot_honoroit_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bot_honoroit_roomid", Label: "Helpdesk room ID", Kind: Text, Required: true, Help: "e.g. !abc:example.com"},
		}},
	{ID: "draupnir", Name: "Draupnir", Category: "Bots", EnableVar: "matrix_bot_draupnir_enabled",
		Description: "Moderation bot (Mjolnir successor).", Doc: "configuring-playbook-bot-draupnir.md",
		Extra:  map[string]string{"matrix_bot_draupnir_zero_touch_deploy": "true"},
		Fields: []Field{{Var: "matrix_bot_draupnir_config_initialManager", Label: "Initial manager MXID", Kind: Text, Required: true, Help: "e.g. @you:example.com"}}},
	{ID: "d4a", Name: "Draupnir for all (D4A)", Category: "Bots", EnableVar: "matrix_appservice_draupnir_for_all_enabled",
		Description: "Draupnir in appservice mode, multiple instances.", Doc: "configuring-playbook-appservice-draupnir-for-all.md",
		Extra:  map[string]string{"matrix_appservice_draupnir_for_all_zero_touch_deploy": "true"},
		Fields: []Field{{Var: "matrix_appservice_draupnir_for_all_config_initialManager", Label: "Initial manager MXID", Kind: Text, Required: true}}},
	{ID: "mjolnir", Name: "Mjolnir", Category: "Bots", EnableVar: "matrix_bot_mjolnir_enabled",
		Description: "The classic moderation bot.", Doc: "configuring-playbook-bot-mjolnir.md",
		Fields: []Field{
			{Var: "matrix_bot_mjolnir_access_token", Label: "Bot access token", Kind: Text, Required: true},
			{Var: "matrix_bot_mjolnir_management_room", Label: "Management room ID", Kind: Text, Required: true},
		}},
	{ID: "meowlnir", Name: "Meowlnir", Category: "Bots", EnableVar: "matrix_bot_meowlnir_enabled",
		Description: "Appservice moderation bot compatible with Draupnir/Mjolnir policy lists.", Doc: "configuring-playbook-bot-meowlnir.md",
		Fields: []Field{{Var: "matrix_bot_meowlnir_bots_custom", Label: "Bots", Kind: YAML,
			Default: "- username: meowlnir_bot\n  displayname: Meowlnir\n  avatar_url: \"\"\n  management_room_auto_create: true\n  management_rooms: []"}}},
	{ID: "buscarron", Name: "Buscarron", Category: "Bots", EnableVar: "matrix_bot_buscarron_enabled", Subdomain: "buscarron",
		Description: "Post web form submissions into a Matrix room.", Doc: "configuring-playbook-bot-buscarron.md",
		Fields: []Field{
			{Var: "matrix_bot_buscarron_password", Label: "Bot password", Kind: Secret, Required: true},
			{Var: "matrix_bot_buscarron_forms", Label: "Forms", Kind: YAML, Required: true,
				Default: "- name: contact\n  room: \"!roomid:{{ matrix_domain }}\"\n  redirect: https://example.com"},
		}},

	// ── Administration ─────────────────────────────────────────────
	{ID: "monitoring", Name: "Prometheus + Grafana", Category: "Administration", EnableVar: "prometheus_enabled", Subdomain: "stats",
		Description: "Metrics and pretty dashboards for your server.", Doc: "configuring-playbook-prometheus-grafana.md",
		Extra: map[string]string{"prometheus_node_exporter_enabled": "true", "prometheus_postgres_exporter_enabled": "true", "grafana_enabled": "true"},
		Fields: []Field{
			{Var: "grafana_default_admin_user", Label: "Grafana admin user", Kind: Text, Default: "admin"},
			{Var: "grafana_default_admin_password", Label: "Grafana admin password", Kind: Secret, Required: true},
		}},
	{ID: "alertmanager-receiver", Name: "Alertmanager receiver", Category: "Administration", EnableVar: "matrix_alertmanager_receiver_enabled",
		Description: "Deliver Prometheus alerts into Matrix rooms.", Doc: "configuring-playbook-alertmanager-receiver.md",
		Fields: []Field{
			{Var: "matrix_alertmanager_receiver_config_matrix_access_token", Label: "Bot access token", Kind: Text, Required: true},
			{Var: "matrix_alertmanager_receiver_config_matrix_room_mapping", Label: "Room mapping", Kind: YAML, Required: true, Default: "alerts: \"!roomid:{{ matrix_domain }}\""},
		}},
	{ID: "rageshake", Name: "Rageshake", Category: "Administration", EnableVar: "matrix_rageshake_enabled", Subdomain: "rageshake",
		Description: "Bug report server for Element clients.", Doc: "configuring-playbook-rageshake.md"},
	{ID: "usage-exporter", Name: "synapse-usage-exporter", Category: "Administration", EnableVar: "matrix_synapse_usage_exporter_enabled", SynapseOnly: true,
		Description: "Collect Synapse usage statistics.", Doc: "configuring-playbook-synapse-usage-exporter.md"},
	{ID: "auto-compressor", Name: "synapse-auto-compressor", Category: "Administration", EnableVar: "matrix_synapse_auto_compressor_enabled", SynapseOnly: true,
		Description: "Periodically compress Synapse's state tables.", Doc: "configuring-playbook-synapse-auto-compressor.md"},
	{ID: "cleanup", Name: "System cleanup", Category: "Administration", EnableVar: "system_cleanup_docker_timer_enabled",
		Description: "Daily timer that removes old Docker images and logs.", Doc: "configuring-playbook-system-cleanup.md"},
	{ID: "borg", Name: "BorgBackup", Category: "Administration", EnableVar: "backup_borg_enabled",
		Description: "Full encrypted, deduplicated server backups including Postgres.", Doc: "configuring-playbook-backup-borg.md",
		Fields: []Field{
			{Var: "backup_borg_location_repositories", Label: "Repositories", Kind: List, Required: true, Help: "e.g. ssh://user@backup.example.com/./repo"},
			{Var: "backup_borg_storage_encryption_passphrase", Label: "Encryption passphrase", Kind: Secret, Required: true},
			{Var: "backup_borg_ssh_key_private", Label: "SSH private key", Kind: YAML, Help: "Optional. First line |, then the key indented by two spaces."},
		}},

	// ── Calls & extras ─────────────────────────────────────────────
	{ID: "element-call", Name: "Element Call", Category: "Calls & extras", EnableVar: "matrix_element_call_enabled", Subdomain: "call.element",
		Description: "Native Matrix group video calls (includes LiveKit).", Doc: "configuring-playbook-element-call.md"},
	{ID: "matrix-rtc", Name: "Matrix RTC stack", Category: "Calls & extras", EnableVar: "matrix_rtc_enabled",
		Description: "LiveKit + JWT service for Element X calls, without the Element Call web app.", Doc: "configuring-playbook-matrix-rtc.md"},
	{ID: "jitsi", Name: "Jitsi Meet", Category: "Calls & extras", EnableVar: "jitsi_enabled", Subdomain: "jitsi",
		Description: "Self-hosted video conferencing.", Doc: "configuring-playbook-jitsi.md"},
	{ID: "etherpad", Name: "Etherpad", Category: "Calls & extras", EnableVar: "etherpad_enabled", Subdomain: "etherpad",
		Description: "Collaborative documents you can embed in rooms.", Doc: "configuring-playbook-etherpad.md"},
	{ID: "cactus", Name: "Cactus Comments", Category: "Calls & extras", EnableVar: "matrix_cactus_comments_enabled",
		Description: "Federated comment system for websites.", Doc: "configuring-playbook-cactus-comments.md",
		Extra: map[string]string{"matrix_cactus_comments_client_enabled": "true"}},
	{ID: "pantalaimon", Name: "Pantalaimon", Category: "Calls & extras", EnableVar: "matrix_pantalaimon_enabled",
		Description: "E2EE-aware proxy for bots and clients without encryption support.", Doc: "configuring-playbook-pantalaimon.md"},
	{ID: "sygnal", Name: "Sygnal push gateway", Category: "Calls & extras", EnableVar: "matrix_sygnal_enabled", Subdomain: "sygnal",
		Description: "Push gateway for your own mobile apps.", Doc: "configuring-playbook-sygnal.md",
		Fields: []Field{{Var: "matrix_sygnal_apps", Label: "Apps", Kind: YAML, Required: true,
			Default: "com.example.myapp.android:\n  type: gcm\n  api_key: your_api_key_for_gcm"}}},
	{ID: "ntfy", Name: "ntfy push server", Category: "Calls & extras", EnableVar: "ntfy_enabled", Subdomain: "ntfy",
		Description: "UnifiedPush notifications without Google. Very useful when Google services are blocked.", Doc: "configuring-playbook-ntfy.md"},
	{ID: "corporal", Name: "Matrix Corporal", Category: "Calls & extras", EnableVar: "matrix_corporal_enabled", SynapseOnly: true,
		Description: "Manage users and rooms from a central policy (advanced).", Doc: "configuring-playbook-matrix-corporal.md",
		Extra: map[string]string{
			"matrix_synapse_ext_password_provider_rest_auth_enabled":  "true",
			"matrix_synapse_ext_password_provider_rest_auth_endpoint": "\"http://matrix-corporal:41080/_matrix/corporal\"",
		},
		Fields: []Field{
			{Var: "matrix_synapse_ext_password_provider_shared_secret_auth_enabled", Label: "Shared secret auth", Kind: Bool, Default: "true"},
			{Var: "matrix_synapse_ext_password_provider_shared_secret_auth_shared_secret", Label: "Shared secret", Kind: Secret, Required: true},
			{Var: "matrix_corporal_policy_provider_config", Label: "Policy provider (JSON)", Kind: YAML, Required: true,
				Default: "|\n  {\n    \"Type\": \"static_file\",\n    \"Path\": \"/etc/matrix-corporal/policy.json\"\n  }"},
		}},
}

// ByID looks a component up by its ID.
func ByID(id string) (Component, bool) {
	for _, c := range Components {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// Preset is a starting stack: a homeserver and the components to tick.
type Preset struct {
	ID          string
	Name        string
	Description string
	Homeserver  string
	Components  []string
}

// Presets are the built-in stacks offered on the Homeserver step.
var Presets = []Preset{
	{"minimal", "Minimal chat", "Synapse with MAS logins, Element Web and Sable: the smallest usual setup.",
		"synapse", []string{"coturn", "element", "sable", "mas"}},
	{"family", "Friends & family", "Chat plus voice and video calls, and an admin panel.",
		"synapse", []string{"coturn", "element", "sable", "element-call", "mas", "ketesa"}},
	{"bridges", "Bridge hub", "Telegram, WhatsApp, Signal and Discord in one place, encrypted, with your own name on the other side.",
		"synapse", []string{"coturn", "element", "sable", "mas", "ketesa", "double-puppet", "bridges-e2ee", "telegram", "whatsapp", "signal", "discord"}},
	{"community", "Public community", "Open sign-up through MAS, three web clients, moderation, antispam and monitoring.",
		"synapse", []string{"coturn", "element", "cinny", "sable", "mas", "draupnir", "simple-antispam", "ketesa", "monitoring"}},
	{"light", "Lightweight", "Tuwunel with Cinny and Sable for a Raspberry Pi or a 1 GB VPS.",
		"tuwunel", []string{"coturn", "cinny", "sable"}},
}
