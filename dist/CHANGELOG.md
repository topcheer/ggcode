## Changelog
* b3015e785532c4cdb3dd03509aaab489c75845a0 feat: A2A client auth negotiation — Discover → NegotiateAuth → setAuth
* 8a3c87333c761a30bfb1411c8509f3efa5dd41a4 feat: A2A enabled by default — use disabled: true to opt out
* 37c8d58d9c936a1b136572aec4fa3aae5a5886bc feat: A2A multi-scheme auth — OAuth2+PKCE, Device Flow, OIDC, mTLS + presets
* 592b32d2bc68280da14e94c6edff3d4c0d9b7b6d feat: A2A push notification callbacks, output mode validation, historyLength
* db187afb4ede923fa8ce54ae97b2c38f866c9a7b feat: A2A task observability — TUI sidebar, daemon follow, IM notifications
* e913ef71d5d18bc0bdc04404c18900919e636166 feat: A2A v1.0 P1 — push notifications, extended card, SSE final field, client methods
* 4a434cf823acc681a0babc38b4c42248f99666fd feat: A2A v1.0 spec alignment — P0 types, methods, and protocol headers
* 70c4992574ac85f1b32585f46d63866450ed05a4 feat: IM /restart and /help slash commands trigger daemon self-restart
* b6c8d2b4a6f05fbead7327f73a987cbaf3817378 feat: IM slash commands for adapter management — /listim /muteim /muteall /muteself
* d6c6224e97735088a3d94ca14add5f5442aabdd2 feat: OAuth2 flows working — Device Flow ✅, PKCE fixes, auto clipboard+browser
* cb5e0513a4a05c3b75c4c2a2d2d866356041d40a feat: OAuth2 token persistence — survives restart, auto-cached
* 5841ce4cfd80a77fe95d8f86edba8873af42c266 feat: PKCE flow uses dynamic port (localhost:0)
* 2ac07a86a410cf7cb1a4bcbf2523516c4519ad8e feat: WebUI IM adapters grouped by platform with full CRUD
* da7c3620446e102f0f75fb6551bb6fcf4107854e feat: WebUI IM merges persisted bindings with runtime for complete view
* 1c9548190f749cb21a50004f9fcb6a3bd8c6465f feat: WebUI IM page with runtime status, bindings, and actions
* 124492ad963309337c965eb955ccbd767f6723df feat: WebUI MCP page shows args, env, runtime status, tools
* 6d8f2885d884a67a60be0cb4bbeb3c484b7e9a4a feat: WebUI impersonate page with preset switch, version, custom headers
* 57bca1a535f03a4123e12ba1b912ae7a8e28bc73 feat: WebUI restart button triggers daemon self-restart and closes window
* 0970c798bac27150ae5294f9c6899bb930649f86 feat: WebUI system prompt page with markdown preview and plain text edit
* 7273fa4649a55d352dd9e258d33faa389171f9f1 feat: WebUI vendor & endpoint full CRUD with API key management
* 20dd48d4c0440e03869ed974ad3afb969b1849ae feat: auto-copy MCP OAuth device code to clipboard
* bc087775e8ad79ec7b49c8d866fdf058f5d77445 feat: built-in WebUI with config management (SPA + REST API)
* 0d96647fc612eeababbfd5aab60d4f020b64e2e4 feat: client auto token acquisition + fix setAuth bug → full interop
* c70d25c62a1372d05a4c6f7dc0f93b041398186f feat: complete A2A auth implementation — all 4 gaps filled
* 435cf9087ceb434e3ef7451c5ba543af8d165a6b feat: config for all OAuth2 scenarios + token cache isolation
* 8537f1fb5b3a1a23b8f3b1bc786635aef8967de7 feat: daemon follow mode handles MCP OAuth device code flow
* c3a63b1a333b207a2ca72a99783039c3b9a96714 feat: embed GitHub OAuth client_id in preset for zero-config PKCE
* ed3d5a9e71c7b00a785ac8c5e09bdfe6f72ab5bf feat: implement openBrowser + robust token parsing + interactive OAuth2 test
* c3689589ab14387660de880f1c3484a4db9d1cda feat: instance-level A2A config override via .ggcode/a2a.yaml
* b5f64b1446a01751894e2bcbabc4075ac681504b feat: use ResolveA2AAuth in root.go for auto client_id + docs update
* b7996582456859c4525019f36d50281ea23c8a7e fix: /muteall no longer mutes the sender's adapter
* c340a69d45018e057772d1dfaa344b6a029d7241 fix: MCP page loads from /api/mcp (with runtime status) instead of /api/config
* b7b37b67fbc9d5d4eb9b1c28f8c6da0dcc09871d fix: WebUI IM page merges static config with runtime status for all adapters
* 5b915324a2cb5defee8987473bd5382fdc0e2b67 fix: WebUI MCP status, http headers display, type-aware add form
* f55de4c5886c55a6e9d856c21a764dc9a5ce8d14 fix: WebUI SPA embed path fix with fallback
* b1843911472c1be88fd8280b606c73ed5e059e46 fix: Windows build — replace syscall.Kill with build-tagged selfSignal()
* 1b650f1655676c0237c64d4082090604a7438062 fix: Windows restart — channel-based trigger replaces selfSignal
* 3fe261995a587d0574e562f31cf86bc2e112712b fix: add json tags to config structs for WebUI API
* 1a42bd8531b7eafb1dd114d0ed11531c8cc4da77 fix: add missing json tags to EndpointConfig, VendorConfig, MCPServerConfig
* 94a41702966cc38537e5a4b35801ac5d2149f5ab fix: correct TestIMConfigShowJSON to use lowercase JSON key
* 4e448d3d7424f6b3213617c86307c504a0774211 fix: include system_prompt in /api/config response
* 7277d754f73b3d8fd760a6f80cc7700f2fb383dd fix: resolve all 851 go vet copylocks warnings in tui package
* 25a046a12a7c6821e9e4a85a92a3769ced9ca188 fix: separate IM config adapters from ghost persisted bindings
* f9cba35d8d6749c7f6f99abe86c4a6fbbf56537b release: v1.1.46
* c45dd631bfec2d409fe3f2453ed9286f16bdafc0 test: A2A E2E integration tests for events, push, auth negotiation
* f1957ead1bc20f55b53955f42bf498a8b5a671d7 test: comprehensive A2A spec alignment test coverage (20 new tests)
* e63c3f2dd055ccb6cdd5d27c8335ace843fc28d1 test: fill A2A test gaps + fix push notifier nesting bug
* 6933a21b4250a33264392836db7a3c72fa7609e7 test: five-instance mesh E2E — all auth methods interop verified
