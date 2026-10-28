import { defineConfig } from "vite";

/**
 * The phase 6 e2e fixture: a small Vite dev project, there to prove that HMR
 * works over the tunnel.
 *
 * `base` has to be THE SAME as the app shell's scope root, and the agent's
 * target has to carry that prefix too
 * (`--target http://127.0.0.1:PORT/PeerPages/`). The reason: the Service
 * Worker strips the scope prefix before sending the path to the agent, while
 * Vite expects prefixed paths; lining the two up also keeps the
 * ROOT-ABSOLUTE paths the page produces inside the scope (the structural
 * boundary in BRIEF section 8).
 *
 * There is NO special HMR setting here, and that is deliberate: if the
 * acceptance criterion is "Vite HMR works over the tunnel", then it has to
 * work without anyone touching their HMR configuration.
 */
export default defineConfig({
  base: process.env.PEERPAGES_FIXTURE_BASE ?? "/PeerPages/",
  server: {
    host: "127.0.0.1",
    strictPort: false,
  },
});
