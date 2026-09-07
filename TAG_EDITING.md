# Server tag editing

Provider adapters exist for AWS EC2 server tags, DigitalOcean Droplet named tags and Hetzner server labels. Core operations persist and enforce tag changes. Compatible runtimes advertise the action, and the shared resource modal provides tag rows with add/remove controls. The operation review shows both complete sets. Older runtimes do not gain this capability implicitly.

The provider request carries complete expected and target tag sets. Submit reads the exact server and compares both status and tags before writing. An unchanged target succeeds from that read. Otherwise SDK mutation success enters observation; only a subsequent exact tag-set match reports completion. Read failures during observation remain pending; failed or lost mutation responses remain uncertain. Mutations are not retried.

AWS preserves reserved aws: tags and uses exact-value deletion before adding/updating labels. Hetzner sends an explicit labels map, including an empty map to clear labels. DigitalOcean creates required tag names, attaches new names and detaches removed names, without deleting account-wide tags. DigitalOcean named tags reject ambiguous casing and path characters. All providers retain bounded tag validation.

These APIs do not provide an atomic compare-and-swap spanning the read and write. AWS/DigitalOcean changes can partially apply; Hetzner replaces the label map. A concurrent cloud edit can race the final read. DigitalOcean can retain a newly created unused tag after a partial failure. No automatic rollback is promised.

Core persistence is implemented in schema 69: expected/target sets are immutable, idempotency compares their contents, fresh inventory tags are checked at request/dispatch, and approval when required by organization policy plus existing RBAC, maintenance and IaC guards apply. Confirmed matching observations update inventory tags without refreshing unrelated resource metadata. Incorrect success claims become uncertain; reconciliation only reads. Database tests cover this workflow. The shared UI and explicit runtime capabilities are implemented; browser checks cover all three providers and IaC-disabled editing.

CLI input validation and documentation are implemented: complete expected/target messages are mandatory, explicit empty sets are accepted, and other actions reject tag fields before login. The CLI has been rebuilt and its race tests/vet pass. The shared UI/review and runtime advertisement are implemented.
