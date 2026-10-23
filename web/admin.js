// ==========================================================================
// Crelan Platform Admin Console Client Engine
// ==========================================================================

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => document.querySelectorAll(selector);


function getWorkloadsViewMode() {
  return state.workloadsViewMode || localStorage.getItem("crelan_workloads_view_mode") || "list";
}

function setWorkloadsViewMode(mode) {
  state.workloadsViewMode = mode;
  localStorage.setItem("crelan_workloads_view_mode", mode);
  renderCurrentView();
}
window.setWorkloadsViewMode = setWorkloadsViewMode;

// Application State
const state = {
  activeView: "overview",
  selectedNamespace: "",
  searchQuery: "",
  perspective: "k8s", // 'k8s' or 'workspace'
  autoRefreshTimer: null,
  cluster: null,
  namespaces: [],
  workspaces: [],
  cachedData: {},
  activeInspector: null,
  activeTerminal: null,
  activeLogs: null,
};

// -------------------------------------------------------------
// Core Request Helper
// -------------------------------------------------------------

async function request(path, options = {}) {
  const opts = {
    credentials: "same-origin",
    ...options,
  };
  const response = await fetch(path, opts);
  if (response.status === 401) {
    window.location.assign("/login");
    throw new Error("Sign in required");
  }
  const isJSON = (response.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await response.json().catch(() => ({})) : await response.text();
  if (!response.ok) {
    let errMsg = `Request failed (${response.status})`;
    if (isJSON && body && body.error) {
      errMsg = body.error;
    } else if (!isJSON && typeof body === "string") {
      if (body.includes("520: Web server is returning an unknown error")) {
        errMsg = "Cloudflare Error (520): Server connection closed or timed out.";
      } else if (body.includes("524: A timeout occurred")) {
        errMsg = "Cloudflare Error (524): Request timed out waiting for server response.";
      } else if (body.includes("<title>")) {
        const titleMatch = body.match(/<title>(.*?)<\/title>/i);
        errMsg = titleMatch ? titleMatch[1].trim() : `Server error (${response.status})`;
      } else {
        errMsg = body.slice(0, 180).trim();
      }
    }
    const err = new Error(errMsg);
    err.status = response.status;
    throw err;
  }
  return body;
}

function escapeHTML(str) {
  if (str === null || str === undefined) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function filterRows(items, fields) {
  if (!items || !Array.isArray(items)) return [];
  if (!state.searchQuery) return items;
  const q = state.searchQuery.toLowerCase();
  return items.filter((item) => {
    return fields.some((f) => {
      const val = item[f];
      if (val === null || val === undefined) return false;
      return String(val).toLowerCase().includes(q);
    });
  });
}

// -------------------------------------------------------------
// Navigation, Routing & Operational Domains
// -------------------------------------------------------------

const DOMAIN_METADATA = {
  k8s: {
    id: "k8s",
    title: "Cluster Core",
    icon: "☸️",
    desc: "Workloads & Infrastructure",
    defaultView: "overview",
  },
  sre: {
    id: "sre",
    title: "SRE & Reliability",
    icon: "📈",
    desc: "SLOs, RED Signals & Chaos",
    defaultView: "sre-slos",
  },
  security: {
    id: "security",
    title: "Security & Compliance",
    icon: "🛡️",
    desc: "CVEs, CIS Hardening & RBAC",
    defaultView: "security",
  },
  ops: {
    id: "ops",
    title: "Platform Ops & Fleet",
    icon: "⚙️",
    desc: "Multi-Cluster, Helm & GitOps",
    defaultView: "cluster-fleet",
  },
};

const VIEW_TO_DOMAIN = {
  // Domain 1: Cluster Core
  overview: "k8s",
  nodes: "k8s",
  pods: "k8s",
  deployments: "k8s",
  statefulsets: "k8s",
  daemonsets: "k8s",
  jobs: "k8s",
  cronjobs: "k8s",
  services: "k8s",
  ingresses: "k8s",
  networkpolicies: "k8s",
  configmaps: "k8s",
  secrets: "k8s",
  pvcs: "k8s",
  pvs: "k8s",
  storageclasses: "k8s",
  namespaces: "k8s",
  events: "k8s",
  "dangling-configs": "k8s",

  // Domain 2: SRE & Reliability
  "sre-slos": "sre",
  "sre-golden-signals": "sre",
  "sre-probes": "sre",
  "sre-resilience": "sre",
  "sre-canary": "sre",
  "sre-chaos": "sre",
  "sre-scorecards": "sre",
  "war-room": "sre",
  "event-storm": "sre",
  autoscaling: "sre",

  // Domain 3: Security & Compliance
  security: "security",
  "image-vulnerabilities": "security",
  rbac: "security",
  "can-i": "security",
  "isolation-matrix": "security",
  "access-control": "security",
  certificates: "security",
  audit: "security",

  // Domain 4: Platform Ops & Fleet
  "cluster-fleet": "ops",
  "helm-releases": "ops",
  crds: "ops",
  copilot: "ops",
  rightsizing: "ops",
  efficiency: "ops",
  databases: "ops",
  "storage-leaks": "ops",
  topology: "ops",
  "upgrade-radar": "ops",
  "gitops-drift": "ops",
  "backup-radar": "ops",
  "cluster-connect": "ops",
  readiness: "ops",
  backup: "ops",
  webhooks: "ops",
  docs: "ops",
};

function getRoute() {
  const hash = window.location.hash.replace(/^#/, "");
  return hash || "overview";
}

function setRoute(route) {
  window.location.hash = route;
}

function switchDomain(domainId, navigateToDefault = true) {
  if (!DOMAIN_METADATA[domainId]) domainId = "k8s";
  state.activeDomain = domainId;
  document.body.dataset.activeDomain = domainId;

  // Update rail domain buttons
  $$(".admin-rail-domain-btn").forEach((btn) => {
    if (btn.dataset.domain === domainId) {
      btn.classList.add("active");
    } else {
      btn.classList.remove("active");
    }
  });

  // Update sidebar domain header badge
  const meta = DOMAIN_METADATA[domainId];
  const iconEl = $("#sidebar-domain-icon");
  const titleEl = $("#sidebar-domain-title");
  const descEl = $("#sidebar-domain-desc");
  if (iconEl) iconEl.textContent = meta.icon;
  if (titleEl) titleEl.textContent = meta.title;
  if (descEl) descEl.textContent = meta.desc;

  if (navigateToDefault) {
    const current = getRoute();
    if (VIEW_TO_DOMAIN[current] !== domainId) {
      setRoute(meta.defaultView);
    }
  }
}

function updateNavigationActive() {
  const current = getRoute();
  state.activeView = current;

  // Sync domain from active view
  const domain = VIEW_TO_DOMAIN[current] || "k8s";
  switchDomain(domain, false);

  $$(".admin-tree-item").forEach((item) => {
    if (item.dataset.view === current) {
      item.classList.add("active");
    } else {
      item.classList.remove("active");
    }
  });
}

// -------------------------------------------------------------
// Cluster Status & Namespace Select
// -------------------------------------------------------------

async function loadClusterHeader() {
  try {
    const info = await request("/api/v1/admin/cluster/info");
    state.cluster = info;
    const isConn = Boolean(info && info.connected);
    $("#sidebar-cluster-name").textContent = isConn ? (info.context || "connected-cluster") : "No cluster";
    $("#sidebar-cluster-tag").textContent = isConn ? "Connected" : "Disconnected";
    $("#topbar-context-name").textContent = isConn ? (info.context || "connected-cluster") : "No cluster connected";
    $("#topbar-k8s-version").textContent = isConn ? (info.server_version || "") : "";

    const dot = $("#topbar-dot");
    const railDot = $("#rail-status-dot");
    const railBadge = $("#rail-cluster-badge");
    if (isConn) {
      dot.style.backgroundColor = "var(--admin-green)";
      railDot.style.backgroundColor = "var(--admin-green)";
      if (railBadge) railBadge.classList.add("active");
    } else {
      dot.style.backgroundColor = "var(--admin-red)";
      railDot.style.backgroundColor = "var(--admin-red)";
      if (railBadge) railBadge.classList.remove("active");
    }

    $("#badge-nodes").textContent = isConn ? (info.nodes_count || 0) : 0;
    $("#badge-pods").textContent = isConn ? (info.pods_count || 0) : 0;
    $("#badge-namespaces").textContent = isConn ? (info.namespaces_count || 0) : 0;
    $("#badge-events").textContent = isConn ? (info.warning_events_count || 0) : 0;

    await loadClusterFleetRail();
  } catch (err) {
    console.warn("Could not load cluster header:", err);
  }
}

function getClusterMonogram(name, alias) {
  const text = (alias || name || "K8").trim();
  const parts = text.split(/[\s\-_.:/]+/);
  if (parts.length >= 2 && parts[0] && parts[1]) {
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }
  return text.substring(0, 2).toUpperCase();
}

async function loadClusterFleetRail() {
  try {
    const fleet = await request("/api/v1/admin/clusters/fleet");
    state.fleet = fleet;
    renderRailClusterList(fleet);
  } catch (err) {
    console.warn("Could not load cluster fleet for rail:", err);
  }
}

function renderRailClusterList(fleet) {
  const container = $("#rail-cluster-list");
  if (!container) return;
  if (!fleet || fleet.length === 0) {
    container.innerHTML = "";
    return;
  }

  container.innerHTML = fleet.map(c => {
    const monogram = getClusterMonogram(c.name, c.alias);
    const color = c.color || "#3b82f6";
    const isActive = Boolean(c.is_active || c.is_current);
    const displayName = c.alias ? `${c.alias} (${c.name})` : c.name;
    const tooltip = `${displayName}\nTier: ${c.environment || "production"}\nStatus: ${c.status || "connected"}\nNodes: ${c.nodes_count || c.node_count || 0}`;
    return `
      <button class="admin-cluster-avatar ${isActive ? 'active' : ''}"
        style="background-color: ${escapeHTML(color)};"
        type="button"
        title="${escapeHTML(tooltip)}"
        data-context="${escapeHTML(c.name)}"
        onclick="handleRailClusterClick('${escapeHTML(c.name)}')">
        ${escapeHTML(monogram)}
      </button>
    `;
  }).join("");
}

async function handleRailClusterClick(contextName) {
  const current = state.cluster && state.cluster.context;
  if (current === contextName) {
    setRoute("cluster-fleet");
    return;
  }
  await switchClusterContext(contextName);
}

async function loadNamespacesDropdown() {
  try {
    const list = await request("/api/v1/admin/namespaces");
    state.namespaces = list;
    const select = $("#admin-namespace-select");
    const current = state.selectedNamespace;

    select.replaceChildren();
    const allOpt = document.createElement("option");
    allOpt.value = "";
    allOpt.textContent = "All Namespaces";
    select.append(allOpt);

    list.forEach((ns) => {
      const opt = document.createElement("option");
      opt.value = ns.name;
      opt.textContent = ns.tenant ? `${ns.name} (tenant)` : ns.name;
      if (ns.name === current) opt.selected = true;
      select.append(opt);
    });
  } catch (err) {
    console.warn("Could not load namespaces dropdown:", err);
  }
}

async function loadWorkspaceMappings() {
  try {
    state.workspaces = await request("/api/v1/admin/workspaces/mapping");
  } catch (err) {
    state.workspaces = [];
  }
}

// -------------------------------------------------------------
// View Renderers
// -------------------------------------------------------------

const OFFLINE_ALLOWED_VIEWS = new Set([
  "docs",
  "cluster-connect",
  "cluster-fleet",
  "audit-trail",
  "admin-tokens",
  "rbac-generator",
  "release-notes",
]);

async function renderCurrentView() {
  updateNavigationActive();
  const container = $("#admin-content-area");
  container.innerHTML = `<div class="admin-empty-state">Loading ${state.activeView}…</div>`;

  if ((!state.cluster || !state.cluster.connected) && !OFFLINE_ALLOWED_VIEWS.has(state.activeView)) {
    const viewTitle = (state.activeView || "Dashboard").replace(/-/g, " ");
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1 style="text-transform: capitalize;">${escapeHTML(viewTitle)}</h1></div>
      </div>
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 60px 20px;">
          <h2 style="font-size: 18px; margin-bottom: 8px;">No Kubernetes Cluster Connected</h2>
          <p style="color: var(--admin-text-muted); max-width: 480px; margin: 0 auto 18px;">
            Connect your Kubernetes cluster using a kubeconfig to inspect and manage nodes, pods, workloads, and cluster resources.
          </p>
          <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
        </div>
      </div>`;
    return;
  }

  try {
    switch (state.activeView) {
      case "overview":
        await renderOverview(container);
        break;
      case "nodes":
        await renderNodes(container);
        break;
      case "pods":
        await renderPods(container);
        break;
      case "deployments":
        await renderDeployments(container);
        break;
      case "statefulsets":
        await renderStatefulSets(container);
        break;
      case "daemonsets":
        await renderDaemonSets(container);
        break;
      case "jobs":
        await renderJobs(container);
        break;
      case "cronjobs":
        await renderCronJobs(container);
        break;
      case "services":
        await renderServices(container);
        break;
      case "ingresses":
        await renderIngresses(container);
        break;
      case "networkpolicies":
        await renderNetworkPolicies(container);
        break;
      case "configmaps":
        await renderConfigMaps(container);
        break;
      case "secrets":
        await renderSecrets(container);
        break;
      case "pvcs":
        await renderPVCs(container);
        break;
      case "pvs":
        await renderPVs(container);
        break;
      case "storageclasses":
        await renderStorageClasses(container);
        break;
      case "namespaces":
        await renderNamespaces(container);
        break;
      case "events":
        await renderEvents(container);
        break;
      case "copilot":
        await renderCopilot(container);
        break;
      case "security":
        await renderSecurityAudit(container);
        break;
      case "rbac":
        await renderRBAC(container);
        break;
      case "rightsizing":
        await renderRightSizing(container);
        break;
      case "databases":
        await renderDatabaseTopology(container);
        break;
      case "storage-leaks":
        await renderStorageLeaks(container);
        break;
      case "efficiency":
        await renderEfficiency(container);
        break;
      case "war-room":
        await renderWarRoom(container);
        break;
      case "audit":
        await renderAuditTrail(container);
        break;
      case "upgrade-radar":
        await renderUpgradeRadar(container);
        break;
      case "topology":
        await renderNetworkTopology(container);
        break;
      case "isolation-matrix":
        await renderTenantIsolationMatrix(container);
        break;
      case "certificates":
        await renderCertificates(container);
        break;
      case "webhooks":
        await renderWebhooks(container);
        break;
      case "dangling-configs":
        await renderDanglingConfigs(container);
        break;
      case "event-storm":
        await renderEventStorm(container);
        break;
      case "autoscaling":
        await renderAutoscaling(container);
        break;
      case "gitops-drift":
        await renderGitOpsDrift(container);
        break;
      case "backup-radar":
        await renderDisasterRecoveryRadar(container);
        break;
      case "cluster-connect":
        renderClusterConnect(container);
        break;
      case "access-control":
        await renderAccessControl(container);
        break;
      case "readiness":
        await renderReadiness(container);
        break;
      case "backup":
        renderBackup(container);
        break;
      case "image-vulnerabilities":
        await renderImageVulnerabilities(container);
        break;
      case "helm-releases":
        await renderHelmReleases(container);
        break;
      case "crds":
        await renderCRDs(container);
        break;
      case "can-i":
        await renderCanI(container);
        break;
      case "cluster-fleet":
        await renderClusterFleet(container);
        break;
      case "sre-slos":
        await renderSRESLOs(container);
        break;
      case "sre-golden-signals":
        await renderSREGoldenSignals(container);
        break;
      case "sre-probes":
        await renderSREProbes(container);
        break;
      case "sre-resilience":
        await renderSREResilience(container);
        break;
      case "sre-canary":
        await renderSRECanaries(container);
        break;
      case "sre-chaos":
        await renderSREChaos(container);
        break;
      case "sre-scorecards":
        await renderSREScorecards(container);
        break;
      case "docs":
        await renderDocs(container);
        break;
      default:
        await renderOverview(container);
    }
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title"><h1 style="text-transform: capitalize;">${escapeHTML(state.activeView)}</h1></div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 60px 20px;">
            <h2 style="font-size: 18px; margin-bottom: 8px;">No Kubernetes Cluster Connected</h2>
            <p style="color: var(--admin-text-muted); max-width: 480px; margin: 0 auto 18px;">
              Connect your Kubernetes cluster using a kubeconfig to inspect and manage nodes, pods, workloads, and cluster resources.
            </p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    container.innerHTML = `
      <div class="admin-empty-state">
        <h3 style="color: var(--admin-red);">Failed to load view</h3>
        <p>${escapeHTML(err.message)}</p>
        <button class="admin-btn admin-btn-primary" onclick="renderCurrentView()" style="margin-top: 10px;">Retry</button>
      </div>`;
  }
}

// -------------------------------------------------------------
// 1. Cluster Overview
// -------------------------------------------------------------

async function renderOverview(container) {
  const [info, nodes, pods] = await Promise.all([
    request("/api/v1/admin/cluster/info").catch(() => ({ connected: false })),
    request("/api/v1/admin/nodes").catch(() => []),
    request("/api/v1/admin/pods").catch(() => []),
  ]);

  let runningPods = 0;
  let pendingPods = 0;
  let failedPods = 0;
  pods.forEach((p) => {
    if (p.has_errors || p.phase === "Failed" || p.phase === "CrashLoopBackOff" || p.phase === "OOMKilled") {
      failedPods++;
    } else if (p.phase === "Pending") {
      pendingPods++;
    } else {
      runningPods++;
    }
  });

  const totalPods = pods.length || 1;
  const runPct = (runningPods / totalPods) * 100;
  const pendPct = (pendingPods / totalPods) * 100;
  const failPct = (failedPods / totalPods) * 100;

  const html = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Overview</h1>
        <span class="admin-view-count">${escapeHTML(info.context || "in-cluster")}</span>
      </div>
      <div class="admin-view-actions">
        <a class="admin-btn" href="#security">Security Audit</a>
        <a class="admin-btn" href="#efficiency">Resource Heatmap</a>
        <a class="admin-btn admin-btn-primary" href="#cluster-connect">Switch / Connect Cluster</a>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cluster Health</span><span>Platform</span></div>
        <div class="admin-stat-value" style="color: ${info.ready_nodes > 0 ? "var(--admin-green)" : "var(--admin-red)"};">
          ${info.connected ? "Healthy" : "Offline"}
        </div>
        <div class="admin-stat-subtext">${escapeHTML(info.server_version || "Kubernetes")} (${escapeHTML(info.platform || "linux/amd64")})</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Ready Nodes</span><span>Infrastructure</span></div>
        <div class="admin-stat-value">${info.ready_nodes} / ${info.nodes_count}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill green" style="width: ${info.nodes_count > 0 ? (info.ready_nodes / info.nodes_count) * 100 : 0}%;"></div>
          </div>
        </div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Workloads (Pods)</span><span>Workloads</span></div>
        <div class="admin-stat-value">${pods.length}</div>
        <div class="admin-breakdown-bar">
          <div class="admin-breakdown-segment running" style="width: ${runPct}%;" title="Running: ${runningPods}"></div>
          <div class="admin-breakdown-segment pending" style="width: ${pendPct}%;" title="Pending: ${pendingPods}"></div>
          <div class="admin-breakdown-segment failed" style="width: ${failPct}%;" title="Failed: ${failedPods}"></div>
        </div>
        <div class="admin-stat-subtext">${runningPods} running · ${failedPods} with errors</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Customer Workspaces</span><span>Tenants</span></div>
        <div class="admin-stat-value">${info.workspaces_count || 0}</div>
        <div class="admin-stat-subtext">${info.deployments_count || 0} active deployments</div>
      </div>
    </div>

    <!-- Cluster Nodes Summary -->
    <div style="margin-bottom: 24px;">
      <div class="admin-view-header">
        <div class="admin-view-title"><h2 style="font-size: 15px; margin: 0;">Cluster Nodes</h2></div>
        <a class="admin-btn admin-btn-sm" href="#nodes">View All Nodes</a>
      </div>
      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Roles</th>
              <th>Version</th>
              <th>Internal IP</th>
              <th>Pods</th>
              <th>CPU Alloc</th>
              <th>Mem Alloc</th>
            </tr>
          </thead>
          <tbody>
            ${nodes.map((n) => `
              <tr onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
                <td class="admin-table-name">${escapeHTML(n.name)}</td>
                <td><span class="admin-badge ${n.status === "Ready" ? "admin-badge-ready" : "admin-badge-error"}"><span class="admin-badge-dot"></span>${escapeHTML(n.status)}</span></td>
                <td>${escapeHTML(n.roles.join(", "))}</td>
                <td>${escapeHTML(n.version)}</td>
                <td>${escapeHTML(n.internal_ip)}</td>
                <td>${n.pods_running} / ${n.pods_capacity}</td>
                <td>${escapeHTML(n.cpu_allocatable)}</td>
                <td>${escapeHTML(n.mem_allocatable)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    </div>

    <!-- Warning Events Preview -->
    <div>
      <div class="admin-view-header">
        <div class="admin-view-title"><h2 style="font-size: 15px; margin: 0;">Recent Cluster Warning Events</h2></div>
        <a class="admin-btn admin-btn-sm" href="#events">View All Events</a>
      </div>
      <div id="overview-events-container">
        <div class="admin-empty-state" style="padding: 20px;">Loading warning events…</div>
      </div>
    </div>
  `;
  container.innerHTML = html;

  try {
    const events = await request("/api/v1/admin/events?type=Warning");
    const evContainer = $("#overview-events-container");
    if (!events.length) {
      evContainer.innerHTML = `<div class="admin-empty-state" style="padding: 20px;">No warning events in the cluster. All workloads operating normally.</div>`;
    } else {
      evContainer.innerHTML = `
        <div class="admin-table-container">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Reason</th>
                <th>Object</th>
                <th>Namespace</th>
                <th>Message</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody>
              ${events.slice(0, 8).map((e) => `
                <tr>
                  <td><span class="admin-badge admin-badge-warning"><span class="admin-badge-dot"></span>${escapeHTML(e.reason)}</span></td>
                  <td><strong>${escapeHTML(e.object_kind)}/${escapeHTML(e.object_name)}</strong></td>
                  <td class="admin-table-ns">${escapeHTML(e.namespace || "-")}</td>
                  <td style="max-width: 400px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
                  <td class="admin-table-age">${escapeHTML(e.age)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>`;
    }
  } catch (e) {}
}

// -------------------------------------------------------------
// 2. Nodes View
// -------------------------------------------------------------

async function renderNodes(container) {
  const nodes = await request("/api/v1/admin/nodes");
  const filtered = filterRows(nodes, ["name", "status", "internal_ip", "version"]);

  const viewMode = getWorkloadsViewMode();
  const toggleHtml = `
    <div class="clickup-view-toggle">
      <button class="clickup-view-btn ${viewMode === 'grid' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('grid')">
        <span>⊞</span> Grid View
      </button>
      <button class="clickup-view-btn ${viewMode === 'list' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('list')">
        <span>☰</span> List View
      </button>
    </div>
  `;

  let contentHtml = "";
  if (viewMode === "grid") {
    contentHtml = `
      <div class="admin-workloads-grid">
        ${filtered.length === 0 ? `<div class="admin-empty-state" style="grid-column: 1 / -1;">No nodes match your filter.</div>` : ""}
        ${filtered.map(n => `
          <div class="admin-workload-card" onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
            <div class="admin-workload-card-header">
              <div>
                <div class="admin-workload-card-title">${escapeHTML(n.name)}</div>
                <div class="admin-workload-card-ns">${escapeHTML(n.roles.join(', ') || 'worker')}</div>
              </div>
              <span class="admin-badge ${n.status === 'Ready' ? 'admin-badge-ready' : 'admin-badge-error'}">
                <span class="admin-badge-dot"></span>${escapeHTML(n.status)}
              </span>
            </div>
            <div class="admin-workload-card-body">
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Version</span>
                <span class="admin-workload-stat-value">${escapeHTML(n.version)}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Internal IP</span>
                <span class="admin-workload-stat-value">${escapeHTML(n.internal_ip)}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Pods Running</span>
                <span class="admin-workload-stat-value">${n.pods_running} / ${n.pods_capacity}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">CPU Allocatable</span>
                <span class="admin-workload-stat-value">${escapeHTML(n.cpu_allocatable)}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Mem Allocatable</span>
                <span class="admin-workload-stat-value">${escapeHTML(n.mem_allocatable)}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Age</span>
                <span class="admin-workload-stat-value">${escapeHTML(n.age)}</span>
              </div>
            </div>
            <div class="admin-workload-card-footer" onclick="event.stopPropagation()">
              <button class="admin-btn admin-btn-xs" onclick="openInspector('node', '', '${escapeHTML(n.name)}')">Inspect YAML</button>
              <button class="admin-btn admin-btn-xs" onclick="openNodeDrainModal('${escapeHTML(n.name)}')">Drain Sim</button>
            </div>
          </div>
        `).join("")}
      </div>
    `;
  } else {
    contentHtml = `
      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Roles</th>
              <th>Version</th>
              <th>OS / Arch</th>
              <th>Internal IP</th>
              <th>Pods</th>
              <th>CPU</th>
              <th>Memory</th>
              <th>Age</th>
              <th style="text-align: right;">Actions</th>
            </tr>
          </thead>
          <tbody>
            ${filtered.length === 0 ? `<tr><td colspan="11" class="admin-empty-state">No nodes match your filter.</td></tr>` : ""}
            ${filtered.map((n) => `
              <tr onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
                <td class="admin-table-name">${escapeHTML(n.name)}</td>
                <td><span class="admin-badge ${n.status === "Ready" ? "admin-badge-ready" : "admin-badge-error"}"><span class="admin-badge-dot"></span>${escapeHTML(n.status)}</span></td>
                <td>${escapeHTML(n.roles.join(", "))}</td>
                <td class="admin-table-ns">${escapeHTML(n.version)}</td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(n.os_image)}</td>
                <td class="admin-table-ns">${escapeHTML(n.internal_ip)}</td>
                <td>${n.pods_running} / ${n.pods_capacity}</td>
                <td>${escapeHTML(n.cpu_allocatable)}</td>
                <td>${escapeHTML(n.mem_allocatable)}</td>
                <td class="admin-table-age">${escapeHTML(n.age)}</td>
                <td>
                  <div class="admin-row-actions" style="justify-content: flex-end;">
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openNodeDrainModal('${escapeHTML(n.name)}')" title="Node Drain & Maintenance Safety Simulator">Drain Sim</button>
                  </div>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Nodes</h1>
        <span class="admin-view-count">${nodes.length}</span>
      </div>
      <div style="display: flex; align-items: center; gap: 10px;">
        ${toggleHtml}
      </div>
    </div>
    ${contentHtml}
  `;
  return;
}

// -------------------------------------------------------------
// 3. Workloads: Pods View
// -------------------------------------------------------------

async function renderPods(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  let pods = [];
  try {
    pods = await request(`/api/v1/admin/pods${ns}`);
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title"><h1>Pods</h1></div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3 style="font-size: 16px; margin-bottom: 8px;">No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); max-width: 440px; margin: 0 auto 16px;">
              Connect your Kubernetes cluster using a kubeconfig to inspect and manage pods and workloads.
            </p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>
      `;
      return;
    }
    throw err;
  }
  const filtered = filterRows(pods, ["name", "namespace", "phase", "node", "pod_ip", "workspace_name", "app_name"]);

  const viewMode = getWorkloadsViewMode();
  const toggleHtml = `
    <div class="clickup-view-toggle">
      <button class="clickup-view-btn ${viewMode === 'grid' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('grid')">
        <span>⊞</span> Grid View
      </button>
      <button class="clickup-view-btn ${viewMode === 'list' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('list')">
        <span>☰</span> List View
      </button>
    </div>
  `;

  if (viewMode === "grid") {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Pods</h1>
          <span class="admin-view-count">${pods.length}</span>
        </div>
        <div class="admin-view-actions" style="display: flex; align-items: center; gap: 10px;">
          ${toggleHtml}
          <button class="admin-btn admin-btn-sm" onclick="renderPods($('#admin-content-area'))">Refresh</button>
        </div>
      </div>
      <div class="admin-workloads-grid">
        ${filtered.length === 0 ? `<div class="admin-empty-state" style="grid-column: 1 / -1;">No pods found in namespace.</div>` : ""}
        ${filtered.map((p) => {
          let badgeClass = "admin-badge-running";
          if (p.phase === "Pending") badgeClass = "admin-badge-pending";
          if (p.has_errors || p.phase === "CrashLoopBackOff" || p.phase === "Failed" || p.phase === "OOMKilled" || p.phase === "Error") badgeClass = "admin-badge-crashloop";
          if (p.phase === "Succeeded" || p.phase === "Completed") badgeClass = "admin-badge-completed";

          return `
            <div class="admin-workload-card ${p.has_errors ? 'is-error' : ''}" onclick="openInspector('pod', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
              <div class="admin-workload-card-header">
                <div>
                  <div class="admin-workload-card-title">${escapeHTML(p.name)}</div>
                  <div class="admin-workload-card-ns">${escapeHTML(p.namespace)}</div>
                </div>
                <span class="admin-badge ${badgeClass}"><span class="admin-badge-dot"></span>${escapeHTML(p.phase)}</span>
              </div>
              <div class="admin-workload-card-body">
                <div class="admin-workload-stat-row">
                  <span class="admin-workload-stat-label">Containers</span>
                  <span class="admin-workload-stat-value">${escapeHTML(p.ready_containers)}</span>
                </div>
                <div class="admin-workload-stat-row">
                  <span class="admin-workload-stat-label">Restarts</span>
                  <span class="admin-workload-stat-value" style="color: ${p.restarts > 0 ? 'var(--admin-amber)' : 'inherit'}; font-weight: ${p.restarts > 0 ? '600' : 'normal'};">${p.restarts}</span>
                </div>
                <div class="admin-workload-stat-row">
                  <span class="admin-workload-stat-label">Node</span>
                  <span class="admin-workload-stat-value">${escapeHTML(p.node || '-')}</span>
                </div>
                <div class="admin-workload-stat-row">
                  <span class="admin-workload-stat-label">Controlled By</span>
                  <span class="admin-workload-stat-value">${escapeHTML(p.controlled_by || '-')}</span>
                </div>
                <div class="admin-workload-stat-row">
                  <span class="admin-workload-stat-label">Age</span>
                  <span class="admin-workload-stat-value">${escapeHTML(p.age)}</span>
                </div>
              </div>
              <div class="admin-workload-card-footer" onclick="event.stopPropagation()">
                <button class="admin-btn admin-btn-xs" onclick="openAIDiagnosis('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">Diagnose</button>
                <button class="admin-btn admin-btn-xs" onclick="openPodLogs('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">Logs</button>
                <button class="admin-btn admin-btn-xs" onclick="openPodTerminal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">Exec</button>
                <button class="admin-btn admin-btn-xs admin-btn-danger" onclick="confirmDeletePod('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">Delete</button>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    `;
    return;
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Pods</h1>
        <span class="admin-view-count">${pods.length}</span>
      </div>
      <div class="admin-view-actions" style="display: flex; align-items: center; gap: 10px;">
        ${toggleHtml}
        <button class="admin-btn admin-btn-sm" onclick="renderPods($('#admin-content-area'))">Refresh</button>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            ${state.perspective === "workspace" ? "<th>Workspace / App</th>" : ""}
            <th>Containers</th>
            <th>Restarts</th>
            <th>Controlled By</th>
            <th>Node</th>
            <th>Status</th>
            <th>Age</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="10" class="admin-empty-state">No pods found in namespace.</td></tr>` : ""}
          ${filtered.map((p) => {
            let badgeClass = "admin-badge-running";
            if (p.phase === "Pending") badgeClass = "admin-badge-pending";
            if (p.has_errors || p.phase === "CrashLoopBackOff" || p.phase === "Failed" || p.phase === "OOMKilled" || p.phase === "Error") badgeClass = "admin-badge-crashloop";
            if (p.phase === "Succeeded" || p.phase === "Completed") badgeClass = "admin-badge-completed";

            return `
              <tr class="${p.has_errors ? "row-error" : ""}">
                <td class="admin-table-name" onclick="openInspector('pod', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
                  ${escapeHTML(p.name)}
                </td>
                <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
                ${state.perspective === "workspace" ? `
                  <td>
                    ${p.workspace_name ? `<span class="admin-ws-tag">${escapeHTML(p.workspace_name)}</span>` : `<span style="color: var(--admin-text-dim);">-</span>`}
                    ${p.app_name ? `<small style="display:block; color: var(--admin-text-muted);">${escapeHTML(p.app_name)}</small>` : ""}
                  </td>` : ""}
                <td>${escapeHTML(p.ready_containers)}</td>
                <td style="color: ${p.restarts > 0 ? "var(--admin-amber)" : "inherit"}; font-weight: ${p.restarts > 0 ? "600" : "normal"};">${p.restarts}</td>
                <td class="admin-table-ns">${escapeHTML(p.controlled_by || "-")}</td>
                <td class="admin-table-ns">${escapeHTML(p.node || "-")}</td>
                <td><span class="admin-badge ${badgeClass}"><span class="admin-badge-dot"></span>${escapeHTML(p.phase)}</span></td>
                <td class="admin-table-age">${escapeHTML(p.age)}</td>
                <td>
                  <div class="admin-row-actions" style="justify-content: flex-end;">
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openAIDiagnosis('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Run Root-Cause Diagnosis">Diagnose</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPostMortem('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Generate Post-Mortem & Runbook">Post-Mortem</button>
                    <a class="admin-action-btn" href="#war-room?ns=${encodeURIComponent(p.namespace)}&pod=${encodeURIComponent(p.name)}" onclick="event.stopPropagation();" title="Open Incident War Room">War Room</a>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPodLogs('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="View Container Logs">Logs</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPodTerminal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Interactive Terminal">Exec</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openEphemeralDebugModal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Inject Ephemeral Debug Container (netshoot, alpine, etc)">Debug</button>
                    <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); confirmDeletePod('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete Pod">Delete</button>
                  </div>
                </td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// 4. Workloads: Deployments View
// -------------------------------------------------------------

async function renderDeployments(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const deps = await request(`/api/v1/admin/deployments${ns}`);
  const filtered = filterRows(deps, ["name", "namespace", "workspace_name", "app_name"]);

  const viewMode = getWorkloadsViewMode();
  const toggleHtml = `
    <div class="clickup-view-toggle">
      <button class="clickup-view-btn ${viewMode === 'grid' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('grid')">
        <span>⊞</span> Grid View
      </button>
      <button class="clickup-view-btn ${viewMode === 'list' ? 'active' : ''}" type="button" onclick="setWorkloadsViewMode('list')">
        <span>☰</span> List View
      </button>
    </div>
  `;

  if (viewMode === "grid") {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Deployments</h1>
          <span class="admin-view-count">${deps.length}</span>
        </div>
        <div style="display: flex; align-items: center; gap: 10px;">
          ${toggleHtml}
        </div>
      </div>
      <div class="admin-workloads-grid">
        ${filtered.length === 0 ? `<div class="admin-empty-state" style="grid-column: 1 / -1;">No deployments found.</div>` : ""}
        ${filtered.map((d) => `
          <div class="admin-workload-card" onclick="openInspector('deployment', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">
            <div class="admin-workload-card-header">
              <div>
                <div class="admin-workload-card-title">${escapeHTML(d.name)}</div>
                <div class="admin-workload-card-ns">${escapeHTML(d.namespace)}</div>
              </div>
              <span class="admin-badge ${d.ready_replicas === d.replicas && d.replicas > 0 ? "admin-badge-ready" : "admin-badge-warning"}">
                ${d.ready_replicas} / ${d.replicas} Ready
              </span>
            </div>
            <div class="admin-workload-card-body">
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Available</span>
                <span class="admin-workload-stat-value">${d.available_replicas}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Conditions</span>
                <span class="admin-workload-stat-value">${escapeHTML(d.conditions.join(", ") || "Progressing")}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Images</span>
                <span class="admin-workload-stat-value" style="word-break: break-all;">${escapeHTML(d.images.join(", ") || "-")}</span>
              </div>
              <div class="admin-workload-stat-row">
                <span class="admin-workload-stat-label">Age</span>
                <span class="admin-workload-stat-value">${escapeHTML(d.age)}</span>
              </div>
            </div>
            <div class="admin-workload-card-footer" onclick="event.stopPropagation()">
              <button class="admin-btn admin-btn-xs" onclick="openScaleModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}', ${d.replicas})">Scale</button>
              <button class="admin-btn admin-btn-xs" onclick="triggerRolloutRestart('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">Restart</button>
              <button class="admin-btn admin-btn-xs" onclick="openRollbackModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">Rollback</button>
              <button class="admin-btn admin-btn-xs admin-btn-danger" onclick="confirmDeleteDeployment('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">Delete</button>
            </div>
          </div>
        `).join("")}
      </div>
    `;
    return;
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Deployments</h1>
        <span class="admin-view-count">${deps.length}</span>
      </div>
      <div style="display: flex; align-items: center; gap: 10px;">
        ${toggleHtml}
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            ${state.perspective === "workspace" ? "<th>Workspace</th>" : ""}
            <th>Replicas</th>
            <th>Available</th>
            <th>Images</th>
            <th>Conditions</th>
            <th>Age</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="9" class="admin-empty-state">No deployments found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr>
              <td class="admin-table-name" onclick="openInspector('deployment', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">
                ${escapeHTML(d.name)}
              </td>
              <td class="admin-table-ns">${escapeHTML(d.namespace)}</td>
              ${state.perspective === "workspace" ? `<td>${d.workspace_name ? `<span class="admin-ws-tag">${escapeHTML(d.workspace_name)}</span>` : "-"}</td>` : ""}
              <td><strong>${d.ready_replicas} / ${d.replicas}</strong></td>
              <td>${d.available_replicas}</td>
              <td class="admin-table-ns" style="max-width: 250px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(d.images.join(", "))}</td>
              <td>${escapeHTML(d.conditions.join(", ") || "Progressing")}</td>
              <td class="admin-table-age">${escapeHTML(d.age)}</td>
              <td>
                <div class="admin-row-actions" style="justify-content: flex-end;">
                  <button class="admin-action-btn" onclick="event.stopPropagation(); openRollbackModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Deployment Revision History & Visual Diff">Rollback</button>
                  <button class="admin-action-btn" onclick="event.stopPropagation(); triggerRolloutRestart('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Rollout Restart">Restart</button>
                  <button class="admin-action-btn" onclick="event.stopPropagation(); openScaleModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}', ${d.replicas})" title="Scale Replicas">Scale</button>
                  <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); confirmDeleteDeployment('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Delete Deployment">Delete</button>
                </div>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// 5. StatefulSets, DaemonSets, Jobs, CronJobs
// -------------------------------------------------------------

async function renderStatefulSets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/statefulsets${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>StatefulSets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Replicas</th><th>Images</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No statefulsets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('statefulset', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><strong>${s.ready_replicas ?? 0} / ${s.replicas ?? 0}</strong></td>
              <td class="admin-table-ns">${escapeHTML((s.images || []).join(", "))}</td>
              <td class="admin-table-age">${escapeHTML(s.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderDaemonSets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/daemonsets${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>DaemonSets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Desired</th><th>Current</th><th>Ready</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No daemonsets found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr onclick="openInspector('daemonset', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">
              <td class="admin-table-name">${escapeHTML(d.name)}</td>
              <td class="admin-table-ns">${escapeHTML(d.namespace)}</td>
              <td>${d.desired ?? 0}</td>
              <td>${d.current ?? 0}</td>
              <td>${d.ready ?? 0}</td>
              <td class="admin-table-age">${escapeHTML(d.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderJobs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/jobs${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Jobs</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Completions</th><th>Duration</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No batch jobs found.</td></tr>` : ""}
          ${filtered.map((j) => `
            <tr onclick="openInspector('job', '${escapeHTML(j.namespace)}', '${escapeHTML(j.name)}')">
              <td class="admin-table-name">${escapeHTML(j.name)}</td>
              <td class="admin-table-ns">${escapeHTML(j.namespace)}</td>
              <td>${j.succeeded ?? 0} / ${j.completions ?? 0}</td>
              <td>${escapeHTML(j.duration || "-")}</td>
              <td class="admin-table-age">${escapeHTML(j.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderCronJobs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/cronjobs${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace", "schedule"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>CronJobs</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Schedule</th><th>Suspend</th><th>Active</th><th>Last Schedule</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No cronjobs found.</td></tr>` : ""}
          ${filtered.map((c) => `
            <tr onclick="openInspector('cronjob', '${escapeHTML(c.namespace)}', '${escapeHTML(c.name)}')">
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
              <td><code>${escapeHTML(c.schedule || "* * * * *")}</code></td>
              <td>${c.suspend ? "Yes" : "No"}</td>
              <td>${c.active ?? 0}</td>
              <td>${escapeHTML(c.last_schedule || "-")}</td>
              <td class="admin-table-age">${escapeHTML(c.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 6. Network: Services, Ingresses, NetworkPolicies
// -------------------------------------------------------------

async function renderServices(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/services${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace", "type", "cluster_ip"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Services</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Cluster IP</th><th>External IP</th><th>Ports</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No services found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('service', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="admin-badge admin-badge-completed">${escapeHTML(s.type || "ClusterIP")}</span></td>
              <td class="admin-table-ns">${escapeHTML(s.cluster_ip || "-")}</td>
              <td>${escapeHTML((s.external_ips || []).join(", ") || "-")}</td>
              <td>${escapeHTML((s.ports || []).join(", ") || "-")}</td>
              <td class="admin-table-age">${escapeHTML(s.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderIngresses(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/ingresses${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Ingresses</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Class</th><th>Hosts</th><th>Addresses</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No ingresses found.</td></tr>` : ""}
          ${filtered.map((i) => `
            <tr onclick="openInspector('ingress', '${escapeHTML(i.namespace)}', '${escapeHTML(i.name)}')">
              <td class="admin-table-name">${escapeHTML(i.name)}</td>
              <td class="admin-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.ingress_class || "-")}</td>
              <td>${escapeHTML((i.hosts || []).join(", ") || "*")}</td>
              <td>${escapeHTML((i.addresses || []).join(", ") || "-")}</td>
              <td class="admin-table-age">${escapeHTML(i.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderNetworkPolicies(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/networkpolicies${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Network Policies</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Pod Selector</th><th>Policy Types</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No network policies found.</td></tr>` : ""}
          ${filtered.map((np) => `
            <tr onclick="openInspector('networkpolicy', '${escapeHTML(np.namespace)}', '${escapeHTML(np.name)}')">
              <td class="admin-table-name">${escapeHTML(np.name)}</td>
              <td class="admin-table-ns">${escapeHTML(np.namespace)}</td>
              <td><code>${escapeHTML(np.pod_selector || "All pods")}</code></td>
              <td>${escapeHTML((np.policy_types || []).join(", ") || "-")}</td>
              <td class="admin-table-age">${escapeHTML(np.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 7. Config & Storage
// -------------------------------------------------------------

async function renderConfigMaps(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/configmaps${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>ConfigMaps</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No configmaps found.</td></tr>` : ""}
          ${filtered.map((cm) => `
            <tr onclick="openInspector('configmap', '${escapeHTML(cm.namespace)}', '${escapeHTML(cm.name)}')">
              <td class="admin-table-name">${escapeHTML(cm.name)}</td>
              <td class="admin-table-ns">${escapeHTML(cm.namespace)}</td>
              <td>${cm.keys_count ?? 0}</td>
              <td class="admin-table-ns">${escapeHTML((cm.keys || []).slice(0, 5).join(", "))}${(cm.keys || []).length > 5 ? "…" : ""}</td>
              <td class="admin-table-age">${escapeHTML(cm.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderSecrets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const raw = await request(`/api/v1/admin/secrets${ns}`).catch(() => []);
  const list = Array.isArray(raw) ? raw : [];
  const filtered = filterRows(list, ["name", "namespace", "type"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Secrets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No secrets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('secret', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="admin-badge admin-badge-completed">${escapeHTML(s.type || "Opaque")}</span></td>
              <td>${s.keys_count ?? 0}</td>
              <td class="admin-table-ns">${escapeHTML((s.keys || []).slice(0, 5).join(", "))}${(s.keys || []).length > 5 ? "…" : ""}</td>
              <td class="admin-table-age">${escapeHTML(s.age || "-")}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderPVCs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  let list = [];
  let errorMsg = "";
  try {
    const raw = await request(`/api/v1/admin/pvcs${ns}`);
    list = Array.isArray(raw) ? raw : [];
  } catch (err) {
    errorMsg = err.message || "Failed to load PersistentVolumeClaims";
  }
  const filtered = filterRows(list, ["name", "namespace", "volume_name", "storage_class"]);

  const errorBanner = errorMsg ? `
    <div style="padding: 12px 16px; background: var(--admin-red-bg); border: 1px solid var(--admin-red-border); border-radius: 8px; color: var(--admin-red); margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between;">
      <span><strong>Error loading volume claims:</strong> ${escapeHTML(errorMsg)}</span>
      <button class="admin-btn admin-btn-sm" onclick="renderPVCs($('#admin-content-area'))">Retry</button>
    </div>
  ` : "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Persistent Volume Claims</h1>
        <span class="admin-view-count">${list.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderPVCs($('#admin-content-area'))">Refresh</button>
      </div>
    </div>
    ${errorBanner}
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Status</th>
            <th>Capacity</th>
            <th>StorageClass</th>
            <th>Volume</th>
            <th>Age</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 && !errorMsg ? `<tr><td colspan="8" class="admin-empty-state">No volume claims found.</td></tr>` : ""}
          ${filtered.map((p) => `
            <tr onclick="openInspector('pvc', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
              <td class="admin-table-name">${escapeHTML(p.name)}</td>
              <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
              <td><span class="admin-badge ${p.status === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(p.status || "Unknown")}</span></td>
              <td>${escapeHTML(p.capacity || "-")}</td>
              <td>${escapeHTML(p.storage_class || "-")}</td>
              <td class="admin-table-ns">${escapeHTML(p.volume_name || "-")}</td>
              <td class="admin-table-age">${escapeHTML(p.age || "-")}</td>
              <td style="text-align: right;">
                <button class="admin-action-btn" onclick="event.stopPropagation(); openInspector('pvc', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Inspect PVC YAML">Inspect</button>
                <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); deletePVC('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete PVC">Delete</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderPVs(container) {
  let list = [];
  let errorMsg = "";
  try {
    const raw = await request("/api/v1/admin/pvs");
    list = Array.isArray(raw) ? raw : [];
  } catch (err) {
    errorMsg = err.message || "Failed to load PersistentVolumes";
  }
  const filtered = filterRows(list, ["name", "storage_class", "status"]);

  const errorBanner = errorMsg ? `
    <div style="padding: 12px 16px; background: var(--admin-red-bg); border: 1px solid var(--admin-red-border); border-radius: 8px; color: var(--admin-red); margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between;">
      <span><strong>Error loading persistent volumes:</strong> ${escapeHTML(errorMsg)}</span>
      <button class="admin-btn admin-btn-sm" onclick="renderPVs($('#admin-content-area'))">Retry</button>
    </div>
  ` : "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Persistent Volumes</h1>
        <span class="admin-view-count">${list.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderPVs($('#admin-content-area'))">Refresh</button>
      </div>
    </div>
    ${errorBanner}
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Capacity</th>
            <th>Access Modes</th>
            <th>Reclaim</th>
            <th>Status</th>
            <th>Claim</th>
            <th>StorageClass</th>
            <th>Age</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 && !errorMsg ? `<tr><td colspan="9" class="admin-empty-state">No persistent volumes found.</td></tr>` : ""}
          ${filtered.map((pv) => `
            <tr onclick="openInspector('pv', '', '${escapeHTML(pv.name)}')">
              <td class="admin-table-name">${escapeHTML(pv.name)}</td>
              <td>${escapeHTML(pv.capacity || "-")}</td>
              <td>${escapeHTML((pv.access_modes || []).join(", ") || "-")}</td>
              <td>${escapeHTML(pv.reclaim_policy || "-")}</td>
              <td><span class="admin-badge ${pv.status === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(pv.status || "Unknown")}</span></td>
              <td class="admin-table-ns">${escapeHTML(pv.claim || "-")}</td>
              <td>${escapeHTML(pv.storage_class || "-")}</td>
              <td class="admin-table-age">${escapeHTML(pv.age || "-")}</td>
              <td style="text-align: right;">
                <button class="admin-action-btn" onclick="event.stopPropagation(); openInspector('pv', '', '${escapeHTML(pv.name)}')" title="Inspect PV YAML">Inspect</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderStorageClasses(container) {
  let list = [];
  let errorMsg = "";
  try {
    const raw = await request("/api/v1/admin/storageclasses");
    list = Array.isArray(raw) ? raw : [];
  } catch (err) {
    errorMsg = err.message || "Failed to load StorageClasses";
  }
  const filtered = filterRows(list, ["name", "provisioner"]);

  const errorBanner = errorMsg ? `
    <div style="padding: 12px 16px; background: var(--admin-red-bg); border: 1px solid var(--admin-red-border); border-radius: 8px; color: var(--admin-red); margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between;">
      <span><strong>Error loading storage classes:</strong> ${escapeHTML(errorMsg)}</span>
      <button class="admin-btn admin-btn-sm" onclick="renderStorageClasses($('#admin-content-area'))">Retry</button>
    </div>
  ` : "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Storage Classes</h1>
        <span class="admin-view-count">${list.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderStorageClasses($('#admin-content-area'))">Refresh</button>
      </div>
    </div>
    ${errorBanner}
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Provisioner</th>
            <th>Reclaim Policy</th>
            <th>Volume Binding</th>
            <th>Expansion</th>
            <th>Default</th>
            <th>Age</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 && !errorMsg ? `<tr><td colspan="8" class="admin-empty-state">No storage classes found.</td></tr>` : ""}
          ${filtered.map((sc) => `
            <tr onclick="openInspector('storageclass', '', '${escapeHTML(sc.name)}')">
              <td class="admin-table-name">${escapeHTML(sc.name)}</td>
              <td class="admin-table-ns">${escapeHTML(sc.provisioner || "-")}</td>
              <td>${escapeHTML(sc.reclaim_policy || "-")}</td>
              <td>${escapeHTML(sc.volume_binding_mode || "-")}</td>
              <td>${sc.allow_volume_expansion ? "Yes" : "No"}</td>
              <td>${sc.is_default ? `<span class="admin-badge admin-badge-ready">Default</span>` : "-"}</td>
              <td class="admin-table-age">${escapeHTML(sc.age || "-")}</td>
              <td style="text-align: right;">
                <button class="admin-action-btn" onclick="event.stopPropagation(); openInspector('storageclass', '', '${escapeHTML(sc.name)}')" title="Inspect StorageClass YAML">Inspect</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}


// -------------------------------------------------------------
// 8. Namespaces & Events
// -------------------------------------------------------------

async function renderNamespaces(container) {
  const list = await request("/api/v1/admin/namespaces");
  const filtered = filterRows(list, ["name", "tenant", "status"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Namespaces</h1><span class="admin-view-count">${list.length}</span></div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="openCreateNamespaceModal()">Create Namespace</button>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Status</th><th>Capsule Tenant</th><th>Workspace</th><th>Age</th><th style="text-align: right;">Action</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No namespaces found.</td></tr>` : ""}
          ${filtered.map((ns) => `
            <tr>
              <td class="admin-table-name" onclick="selectNamespaceAndFilter('${escapeHTML(ns.name)}')">${escapeHTML(ns.name)}</td>
              <td><span class="admin-badge ${ns.status === "Active" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(ns.status)}</span></td>
              <td>${ns.tenant ? `<span class="admin-badge admin-badge-completed">Capsule: ${escapeHTML(ns.tenant)}</span>` : `<span style="color: var(--admin-text-dim);">-</span>`}</td>
              <td>${ns.workspace_id ? `<span class="admin-ws-tag">WS #${escapeHTML(ns.workspace_id)}</span>` : "-"}</td>
              <td class="admin-table-age">${escapeHTML(ns.age)}</td>
              <td style="text-align: right;">
                ${ns.name !== "default" && ns.name !== "kube-system" ? `<button class="admin-action-btn btn-delete" onclick="deleteNamespace('${escapeHTML(ns.name)}')" title="Delete Namespace">Delete</button>` : ""}
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function selectNamespaceAndFilter(name) {
  state.selectedNamespace = name;
  $("#admin-namespace-select").value = name;
  setRoute("pods");
}

async function renderEvents(container) {
  const ns = state.selectedNamespace ? `&namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/events?type=${ns}`);
  const filtered = filterRows(list, ["reason", "message", "object_name", "object_kind", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Cluster Events</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Type</th><th>Reason</th><th>Involved Object</th><th>Namespace</th><th>Message</th><th>Count</th><th>Last Seen</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No events found.</td></tr>` : ""}
          ${filtered.map((e) => `
            <tr>
              <td><span class="admin-badge ${e.type === "Warning" ? "admin-badge-warning" : "admin-badge-completed"}">${escapeHTML(e.type)}</span></td>
              <td><strong>${escapeHTML(e.reason)}</strong></td>
              <td class="admin-table-ns"><strong>${escapeHTML(e.object_kind)}</strong>/${escapeHTML(e.object_name)}</td>
              <td class="admin-table-ns">${escapeHTML(e.namespace || "-")}</td>
              <td style="max-width: 450px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
              <td>${e.count}</td>
              <td class="admin-table-age">${escapeHTML(e.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 9. Platform Intelligence Views (Security, DB, Leaks, Efficiency)
// -------------------------------------------------------------

async function renderSecurityAudit(container) {
  const audit = await request("/api/v1/admin/security/audit");

  let scoreColor = "green";
  if (audit.score < 60) scoreColor = "red";
  else if (audit.score < 85) scoreColor = "amber";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Capsule Tenant Radar & CIS Security Scanner</h1></div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="setRoute('rbac')">RBAC Blast Radius</button>
        <button class="admin-btn admin-btn-primary" onclick="renderSecurityAudit($('#admin-content-area'))">Re-scan Cluster</button>
      </div>
    </div>

    <div class="admin-score-banner">
      <div class="admin-score-circle ${scoreColor}">${audit.score}%</div>
      <div class="admin-score-details">
        <h3>CIS Kubernetes Security Posture: ${audit.score >= 80 ? "Hardened" : "Hardening Recommended"}</h3>
        <p>${audit.total_pods_scanned} active workloads evaluated · ${audit.passed_checks} checks passed · ${audit.failed_checks} policy findings</p>
        <p style="margin-top: 6px;">Capsule Tenant Isolation: <strong style="color: ${audit.tenant_isolation.status === "Secure" ? "var(--admin-green)" : "var(--admin-amber)"};">${escapeHTML(audit.tenant_isolation.status)}</strong> (${audit.tenant_isolation.enforced_namespaces}/${audit.tenant_isolation.total_tenant_namespaces} tenant namespaces enforced with NetworkPolicy)</p>
      </div>
    </div>

    <div class="admin-overview-grid" style="margin-top: 20px;">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Critical Findings</span><span>CIS 5.2</span></div>
        <div class="admin-stat-value" style="color: ${audit.critical_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.critical_count || 0}</div>
        <div class="admin-stat-subtext">Immediate privilege risks</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Secret Leak Guard</span><span>Tokens & Keys</span></div>
        <div class="admin-stat-value" style="color: ${audit.secret_leaks_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.secret_leaks_count || 0}</div>
        <div class="admin-stat-subtext">Plaintext secrets in env/ConfigMaps</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Root Containers</span><span>UID 0</span></div>
        <div class="admin-stat-value" style="color: ${audit.root_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${audit.root_count || 0}</div>
        <div class="admin-stat-subtext">Workloads missing runAsNonRoot</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Privileged Pods</span><span>SecurityContext</span></div>
        <div class="admin-stat-value" style="color: ${audit.privileged_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.privileged_count || 0}</div>
        <div class="admin-stat-subtext">Container privileged: true</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Security & Isolation Findings</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Severity</th>
            <th>Rule</th>
            <th>Workload / Pod</th>
            <th>Namespace</th>
            <th>Description</th>
            <th>Remediation & Action</th>
          </tr>
        </thead>
        <tbody>
          ${audit.issues.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">Zero security violations detected in active workloads.</td></tr>` : ""}
          ${audit.issues.map((i) => `
            <tr>
              <td><span class="admin-badge ${i.severity === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(i.severity.toUpperCase())}</span></td>
              <td><strong>${escapeHTML(i.rule)}</strong></td>
              <td class="admin-table-name" onclick="openInspector('pod', '${escapeHTML(i.namespace)}', '${escapeHTML(i.pod_name)}')">${escapeHTML(i.pod_name || i.workload_name || "-")}</td>
              <td class="admin-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.description)}</td>
              <td>
                <div style="font-size: 11px; color: var(--admin-accent); margin-bottom: 6px;">${escapeHTML(i.remediation)}</div>
                ${i.can_auto_harden ? `
                  <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="hardenWorkload('${escapeHTML(i.namespace)}', '${escapeHTML(i.workload_name)}')">1-Click Harden</button>
                ` : ""}
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function hardenWorkload(namespace, deploymentName) {
  if (!confirm(`Apply CIS Workload Hardening to deployment "${deploymentName}" in namespace "${namespace}"? This sets runAsNonRoot: true, drops all capabilities, and sets allowPrivilegeEscalation: false.`)) {
    return;
  }
  try {
    const res = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/harden`, {
      method: "POST",
    });
    alert(res.message || "Workload successfully hardened.");
    renderSecurityAudit($("#admin-content-area"));
  } catch (err) {
    alert("Hardening failed: " + err.message);
  }
}

async function renderRBAC(container) {
  const rbac = await request("/api/v1/admin/security/rbac");

  let riskColor = "green";
  let riskTitle = "Low Privilege Risk";
  if (rbac.risk_score >= 60) {
    riskColor = "red";
    riskTitle = "High Blast Radius Detected";
  } else if (rbac.risk_score >= 30) {
    riskColor = "amber";
    riskTitle = "Moderate Privilege Exposure";
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>RBAC Blast Radius & Privilege Escalation Analyzer</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderRBAC($('#admin-content-area'))">Re-scan RBAC</button>
    </div>

    <div class="admin-score-banner">
      <div class="admin-score-circle ${riskColor}">${rbac.risk_score}</div>
      <div class="admin-score-details">
        <h3>Cluster Privilege Risk: ${riskTitle}</h3>
        <p>${rbac.total_bindings_scanned} role bindings evaluated across cluster scopes.</p>
        <p style="margin-top: 6px; color: var(--admin-text-muted);">
          <strong>${rbac.cluster_admin_count}</strong> cluster-admin bindings ·
          <strong>${rbac.privilege_escalation_count}</strong> privilege escalation paths ·
          <strong>${rbac.wildcard_permissions_count}</strong> wildcard roles ·
          <strong>${rbac.cross_tenant_bindings_count}</strong> cross-tenant leaks
        </p>
      </div>
    </div>

    <div class="admin-overview-grid" style="margin-top: 20px;">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>cluster-admin</span><span>Superuser</span></div>
        <div class="admin-stat-value" style="color: ${rbac.cluster_admin_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.cluster_admin_count}</div>
        <div class="admin-stat-subtext">Bindings granting full cluster control</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Escalation Verbs</span><span>bind / escalate</span></div>
        <div class="admin-stat-value" style="color: ${rbac.privilege_escalation_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.privilege_escalation_count}</div>
        <div class="admin-stat-subtext">Ability to grant oneself permissions</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Wildcard Roles</span><span>* on *</span></div>
        <div class="admin-stat-value" style="color: ${rbac.wildcard_permissions_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${rbac.wildcard_permissions_count}</div>
        <div class="admin-stat-subtext">Broad wildcard verb/resource rules</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cross-Tenant Leaks</span><span>Capsule Boundary</span></div>
        <div class="admin-stat-value" style="color: ${rbac.cross_tenant_bindings_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.cross_tenant_bindings_count}</div>
        <div class="admin-stat-subtext">Tenant SAs with external cluster access</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">High-Risk RBAC Bindings</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Risk Level</th>
            <th>Binding Name</th>
            <th>Type</th>
            <th>Role Ref</th>
            <th>Subjects Bound</th>
            <th>Risk Analysis</th>
            <th>Remediation</th>
          </tr>
        </thead>
        <tbody>
          ${rbac.high_risk_bindings.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No high-risk bindings detected.</td></tr>` : ""}
          ${rbac.high_risk_bindings.map((b) => `
            <tr>
              <td><span class="admin-badge ${b.risk_level === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(b.risk_level.toUpperCase())}</span></td>
              <td><strong>${escapeHTML(b.name)}</strong></td>
              <td><span class="admin-kind-badge">${escapeHTML(b.kind)}</span></td>
              <td><code>${escapeHTML(b.role_ref)}</code></td>
              <td>
                <div style="display: flex; flex-wrap: wrap; gap: 4px;">
                  ${b.subjects.map((s) => `<span class="admin-tag-chip">${escapeHTML(s)}</span>`).join("")}
                </div>
              </td>
              <td>
                <ul style="margin: 0; padding-left: 16px; font-size: 11px; color: var(--admin-red);">
                  ${b.risk_reasons.map((r) => `<li>${escapeHTML(r)}</li>`).join("")}
                </ul>
              </td>
              <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(b.remediation)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Service Account Blast Radius</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Service Account</th>
            <th>Namespace</th>
            <th>Tenant Space?</th>
            <th>Bound Roles</th>
            <th>Risk Exposure</th>
          </tr>
        </thead>
        <tbody>
          ${rbac.service_account_risks.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No elevated service accounts detected.</td></tr>` : ""}
          ${rbac.service_account_risks.map((sa) => `
            <tr>
              <td><strong>${escapeHTML(sa.service_account)}</strong></td>
              <td class="admin-table-ns">${escapeHTML(sa.namespace)}</td>
              <td>
                ${sa.is_tenant ? `<span class="admin-badge admin-badge-accent">Tenant Workspace</span>` : `<span class="admin-badge">System</span>`}
              </td>
              <td>
                ${sa.roles_bound.map((r) => `<code>${escapeHTML(r)}</code>`).join(" ")}
              </td>
              <td style="color: var(--admin-accent); font-size: 11px;">${escapeHTML(sa.risk_summary)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

async function renderRightSizing(container) {
  const sizing = await request("/api/v1/admin/ai/rightsizing");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>AI Resource Right-Sizing & Cost Advisor</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderRightSizing($('#admin-content-area'))">Re-evaluate Resources</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Estimated Monthly Waste</span><span>Cloud Costs</span></div>
        <div class="admin-stat-value" style="color: ${sizing.estimated_monthly_savings_usd > 0 ? "var(--admin-accent)" : "var(--admin-green)"};">
          $${sizing.estimated_monthly_savings_usd.toFixed(2)}
        </div>
        <div class="admin-stat-subtext">Estimated potential savings / month</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Overprovisioned</span><span>High Allocation</span></div>
        <div class="admin-stat-value" style="color: ${sizing.overprovisioned_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">
          ${sizing.overprovisioned_count}
        </div>
        <div class="admin-stat-subtext">Workloads exceeding baselines</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Uncapped Workloads</span><span>Missing Limits</span></div>
        <div class="admin-stat-value" style="color: ${sizing.missing_limits_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${sizing.missing_limits_count}</div>
        <div class="admin-stat-subtext">Containers at risk of eviction</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Evaluated Deployments</span><span>Total</span></div>
        <div class="admin-stat-value">${sizing.total_deployments}</div>
        <div class="admin-stat-subtext">Cluster waste rate: ${sizing.estimated_cluster_waste_percent}%</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Workload Right-Sizing Recommendations</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Deployment</th>
            <th>Namespace</th>
            <th>Current (Req / Lim)</th>
            <th>Recommended Baseline</th>
            <th>Est. Savings</th>
            <th>Risk / Efficiency</th>
            <th>Reasoning</th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          ${sizing.recommendations.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No deployments found to analyze.</td></tr>` : ""}
          ${sizing.recommendations.map((r) => `
            <tr>
              <td class="admin-table-name" onclick="openInspector('deployment', '${escapeHTML(r.namespace)}', '${escapeHTML(r.deployment_name)}')">
                <strong>${escapeHTML(r.deployment_name)}</strong>
                <div style="font-size: 10px; color: var(--admin-text-muted);">${escapeHTML(r.container_name)}</div>
              </td>
              <td class="admin-table-ns">${escapeHTML(r.namespace)}</td>
              <td>
                <div style="font-size: 11px;">CPU: <code>${escapeHTML(r.current_cpu_request)}</code> / <code>${escapeHTML(r.current_cpu_limit)}</code></div>
                <div style="font-size: 11px;">RAM: <code>${escapeHTML(r.current_mem_request)}</code> / <code>${escapeHTML(r.current_mem_limit)}</code></div>
              </td>
              <td>
                <div style="font-size: 11px; color: var(--admin-green);">CPU: <strong>${escapeHTML(r.recommended_cpu_request)}</strong> / <strong>${escapeHTML(r.recommended_cpu_limit)}</strong></div>
                <div style="font-size: 11px; color: var(--admin-green);">RAM: <strong>${escapeHTML(r.recommended_mem_request)}</strong> / <strong>${escapeHTML(r.recommended_mem_limit)}</strong></div>
              </td>
              <td>
                ${r.monthly_savings_usd > 0 ? `<strong style="color: var(--admin-green);">+$${r.monthly_savings_usd.toFixed(2)}/mo</strong>` : "-"}
              </td>
              <td>
                <span class="admin-badge ${r.risk_level === "High Waste" ? "admin-badge-warning" : r.risk_level === "Uncapped" ? "admin-badge-crashloop" : "admin-badge-active"}">
                  ${escapeHTML(r.risk_level)}
                </span>
              </td>
              <td style="font-size: 11px; max-width: 240px;">${escapeHTML(r.reason)}</td>
              <td>
                <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="openRightSizeModal('${escapeHTML(r.namespace)}', '${escapeHTML(r.deployment_name)}', '${escapeHTML(r.recommended_cpu_request)}', '${escapeHTML(r.recommended_cpu_limit)}', '${escapeHTML(r.recommended_mem_request)}', '${escapeHTML(r.recommended_mem_limit)}', ${r.monthly_savings_usd})">
                  Apply Sizing
                </button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

function openRightSizeModal(namespace, deploymentName, cpuReq, cpuLim, memReq, memLim, savings) {
  const modal = $("#admin-rightsize-modal");
  $("#rightsize-dep-name").textContent = deploymentName;
  $("#rightsize-dep-ns").textContent = namespace;
  $("#rightsize-cpu-req").value = cpuReq || "100m";
  $("#rightsize-cpu-lim").value = cpuLim || "500m";
  $("#rightsize-mem-req").value = memReq || "128Mi";
  $("#rightsize-mem-lim").value = memLim || "512Mi";
  $("#rightsize-savings-note").textContent = savings > 0 ? `Est. monthly savings: ~$${savings.toFixed(2)}/month per replica.` : "";
  modal.dataset.namespace = namespace;
  modal.dataset.deployment = deploymentName;
  modal.classList.add("open");
}

async function renderCopilot(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Natural Language AI K8s Copilot</h1></div>
    </div>

    <div class="admin-copilot-container">
      <div class="admin-copilot-hero">
        <div class="admin-copilot-hero-title">
          <span class="admin-copilot-sparkle">AI</span>
          <h2>Text-to-K8s Cluster Intelligence</h2>
        </div>
        <p>Ask natural questions to inspect crashing workloads, evaluate security posture, analyze RBAC blast radius, or find cost savings.</p>

        <form id="copilot-main-form" style="margin-top: 16px;">
          <div style="display: flex; gap: 8px;">
            <input class="text-input" id="copilot-main-input" placeholder="e.g. 'Which pods are crashing?', 'Check secret leaks', 'Show cluster-admin bindings'..." style="flex: 1; font-size: 14px; padding: 10px 14px;" autocomplete="off">
            <button class="admin-btn admin-btn-primary" type="submit" style="padding: 0 20px;">Ask Copilot</button>
          </div>
        </form>

        <div class="admin-quick-prompts" style="margin-top: 12px;">
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Which pods are crashing or restarting?">Crashing pods</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Show me security vulnerabilities and root containers">Security scan</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Check cluster-admin bindings and RBAC blast radius">RBAC blast radius</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Find overprovisioned workloads and right-size recommendations">Cost right-sizing</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Show all warning events across the cluster">Warning events</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Are there any plaintext secret leaks in env or ConfigMaps?">Secret leaks</button>
        </div>
      </div>

      <div id="copilot-main-results" style="margin-top: 24px;"></div>
    </div>
  `;

  const form = $("#copilot-main-form");
  const input = $("#copilot-main-input");
  const results = $("#copilot-main-results");

  async function executeCopilotQuery(q) {
    if (!q || !q.trim()) return;
    input.value = q;
    results.innerHTML = `<div class="admin-empty-state">Evaluating query against cluster state…</div>`;

    try {
      const resp = await request("/api/v1/admin/ai/copilot", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: q }),
      });

      let matchesHtml = "";
      if (resp.matches && resp.matches.length > 0) {
        matchesHtml = `
          <h3 style="font-size: 14px; margin: 20px 0 10px;">Matching Resources (${resp.matches.length})</h3>
          <div class="admin-table-container">
            <table class="admin-table">
              <thead><tr><th>Kind</th><th>Name</th><th>Namespace</th><th>Status</th><th>Details</th><th>Action</th></tr></thead>
              <tbody>
                ${resp.matches.map((m) => `
                  <tr>
                    <td><span class="admin-kind-badge">${escapeHTML(m.kind)}</span></td>
                    <td class="admin-table-name"><strong>${escapeHTML(m.name)}</strong></td>
                    <td class="admin-table-ns">${escapeHTML(m.namespace || "-")}</td>
                    <td><span class="admin-badge ${m.status === "CrashLoopBackOff" || m.status === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(m.status)}</span></td>
                    <td style="font-size: 11px;">${escapeHTML(m.reason ? `${m.reason}: ` : "")}${escapeHTML(m.message || "")}</td>
                    <td>
                      ${m.link ? `<a class="admin-btn admin-btn-sm" href="${escapeHTML(m.link)}">Inspect</a>` : ""}
                    </td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          </div>
        `;
      }

      let actionsHtml = "";
      if (resp.suggested_actions && resp.suggested_actions.length > 0) {
        actionsHtml = `
          <div style="margin-top: 20px;">
            <h3 style="font-size: 14px; margin-bottom: 10px;">Suggested Actions</h3>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
              ${resp.suggested_actions.map((act) => `
                <div class="admin-remediation-card" style="flex: 1; min-width: 260px;">
                  <div class="admin-remediation-info">
                    <strong>${escapeHTML(act.title)}</strong>
                    <span>${escapeHTML(act.description)}</span>
                  </div>
                  ${act.route ? `<a class="admin-btn admin-btn-primary admin-btn-sm" href="${escapeHTML(act.route)}">Execute</a>` : ""}
                </div>
              `).join("")}
            </div>
          </div>
        `;
      }

      results.innerHTML = `
        <div class="admin-ai-banner">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
            <span class="admin-tag-chip" style="color: var(--admin-accent); font-weight: 700;">Intent: ${escapeHTML(resp.intent)}</span>
            <span style="font-size: 11px; color: var(--admin-text-muted);">Query: "${escapeHTML(resp.query)}"</span>
          </div>
          <p style="margin: 0; font-size: 13px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(resp.summary)}</p>
        </div>
        ${actionsHtml}
        ${matchesHtml}
      `;
    } catch (err) {
      results.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Query failed: ${escapeHTML(err.message)}</div>`;
    }
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    executeCopilotQuery(input.value);
  });

  $$(".admin-quick-prompts button").forEach((btn) => {
    btn.addEventListener("click", () => {
      executeCopilotQuery(btn.dataset.prompt);
    });
  });

  executeCopilotQuery("Which pods are crashing or restarting?");
}

async function renderDatabaseTopology(container) {
  const topo = await request("/api/v1/admin/databases/topology");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Managed Database & Operator Topology</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderDatabaseTopology($('#admin-content-area'))">Refresh</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>PostgreSQL Clusters</span><span>CloudNativePG</span></div>
        <div class="admin-stat-value">${topo.postgres_clusters ? topo.postgres_clusters.length : 0}</div>
        <div class="admin-stat-subtext">3-node HA with streaming replication</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>MySQL Clusters</span><span>InnoDB</span></div>
        <div class="admin-stat-value">${topo.mysql_clusters ? topo.mysql_clusters.length : 0}</div>
        <div class="admin-stat-subtext">3-instance quorum cluster</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Storage Leaks</span><span>Orphan Claims</span></div>
        <div class="admin-stat-value" style="color: ${topo.storage_leaks && topo.storage_leaks.length > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">
          ${topo.storage_leaks ? topo.storage_leaks.length : 0}
        </div>
        <div class="admin-stat-subtext">Unmounted tenant claims</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">PostgreSQL Instances (CloudNativePG)</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (Read/Write)</th><th>Standbys</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.postgres_clusters || topo.postgres_clusters.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No CloudNativePG database clusters provisioned yet.</td></tr>` : ""}
          ${(topo.postgres_clusters || []).map((c) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
              <td><span class="admin-badge admin-badge-ready"><span class="admin-badge-dot"></span>${escapeHTML(c.status)}</span></td>
              <td><strong>${escapeHTML(c.primary || `${c.name}-1`)}</strong></td>
              <td class="admin-table-ns">${escapeHTML((c.replicas || []).join(", "))}</td>
              <td><span class="admin-badge admin-badge-completed">WAL Active</span></td>
              <td class="admin-table-age">${escapeHTML(c.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">MySQL InnoDB Clusters</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (R/W)</th><th>Replicas</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.mysql_clusters || topo.mysql_clusters.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No MySQL InnoDB clusters provisioned yet.</td></tr>` : ""}
          ${(topo.mysql_clusters || []).map((m) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(m.name)}</td>
              <td class="admin-table-ns">${escapeHTML(m.namespace)}</td>
              <td><span class="admin-badge admin-badge-ready"><span class="admin-badge-dot"></span>${escapeHTML(m.status)}</span></td>
              <td><strong>${escapeHTML(m.primary || `${m.name}-0`)}</strong></td>
              <td class="admin-table-ns">${escapeHTML((m.replicas || []).join(", "))}</td>
              <td><span class="admin-badge admin-badge-completed">Schedule Active</span></td>
              <td class="admin-table-age">${escapeHTML(m.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderStorageLeaks(container) {
  const topo = await request("/api/v1/admin/databases/topology");
  const leaks = topo.storage_leaks || [];

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Storage Leak Detector</h1>
        <span class="admin-view-count">${leaks.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="renderStorageLeaks($('#admin-content-area'))">Scan for Leaks</button>
      </div>
    </div>
    <p style="color: var(--admin-text-muted); font-size: 12px; margin-bottom: 16px;">
      Persistent Volume Claims (PVCs) created by customer apps that are currently unmounted and retained after workload deletion. Purging frees underlying cloud storage disk resources.
    </p>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Claim Name</th><th>Namespace</th><th>Capacity</th><th>StorageClass</th><th>Reason</th><th>Age</th><th style="text-align: right;">Purge</th></tr></thead>
        <tbody>
          ${leaks.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">Clean cluster. No orphaned persistent volume claims detected.</td></tr>` : ""}
          ${leaks.map((item) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(item.pvc_name)}</td>
              <td class="admin-table-ns">${escapeHTML(item.namespace)}</td>
              <td><strong>${escapeHTML(item.capacity || "5Gi")}</strong></td>
              <td>${escapeHTML(item.storage_class || "standard")}</td>
              <td style="color: var(--admin-amber); font-size: 11px;">${escapeHTML(item.reason)}</td>
              <td class="admin-table-age">${escapeHTML(item.age)}</td>
              <td style="text-align: right;">
                <button class="admin-btn admin-btn-danger admin-btn-sm" onclick="deletePVC('${escapeHTML(item.namespace)}', '${escapeHTML(item.pvc_name)}')">Purge Volume</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderEfficiency(container) {
  const eff = await request("/api/v1/admin/resources/efficiency");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Cluster Resource Efficiency & Heatmap</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderEfficiency($('#admin-content-area'))">Recalculate</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>CPU Requested</span><span>Total</span></div>
        <div class="admin-stat-value">${escapeHTML(eff.cpu_requests_total)}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill ${eff.cpu_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.cpu_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="admin-stat-subtext">${eff.cpu_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_cpu_allocatable)} allocatable</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Memory Requested</span><span>Total</span></div>
        <div class="admin-stat-value">${escapeHTML(eff.mem_requests_total)}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill ${eff.mem_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.mem_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="admin-stat-subtext">${eff.mem_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_mem_allocatable)} allocatable</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">Workspace Allocation Breakdown</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Workspace</th><th>Namespace</th><th>Pods</th><th>CPU Request</th><th>CPU Limit</th><th>Mem Request</th><th>Mem Limit</th><th>Efficiency</th></tr></thead>
        <tbody>
          ${(eff.workspaces || []).length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No active workspace workloads found.</td></tr>` : ""}
          ${(eff.workspaces || []).map((w) => `
            <tr>
              <td class="admin-table-name"><span class="admin-ws-tag">${escapeHTML(w.workspace_name)}</span></td>
              <td class="admin-table-ns">${escapeHTML(w.namespace)}</td>
              <td>${w.pods_count}</td>
              <td>${escapeHTML(w.cpu_request)}</td>
              <td>${escapeHTML(w.cpu_limit)}</td>
              <td>${escapeHTML(w.mem_request)}</td>
              <td>${escapeHTML(w.mem_limit)}</td>
              <td><span class="admin-badge ${w.efficiency === "Optimal" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(w.efficiency)}</span></td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 10. Platform Operations: Connect, Readiness, Backup
// -------------------------------------------------------------

function renderClusterConnect(container) {
  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Connect Deployment Cluster</h1></div></div>
    <div style="max-width: 820px; background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 9px; padding: 24px; box-shadow: var(--admin-shadow-sm);">
      <div style="display: flex; gap: 8px; margin-bottom: 20px; border-bottom: 1px solid var(--admin-border); padding-bottom: 12px;">
        <button class="admin-btn admin-btn-primary" id="tab-btn-kubeconfig" type="button" style="font-size: 12px;">1. Kubeconfig YAML</button>
        <button class="admin-btn" id="tab-btn-token" type="button" style="font-size: 12px;">2. ServiceAccount Token</button>
        <button class="admin-btn" id="tab-btn-agent" type="button" style="font-size: 12px;">3. In-Cluster Agent (Tunnel)</button>
      </div>

      <!-- Tab 1: Kubeconfig YAML -->
      <div id="tab-content-kubeconfig">
        <p style="color: var(--admin-text-muted); font-size: 13px; margin-top: 0; line-height: 1.6;">
          Connect the Kubernetes cluster that customer workspaces deploy to. Paste a self-contained kubeconfig with certificate data embedded.
        </p>
        <form id="admin-connect-form">
          <label class="form-label" for="cluster-kubeconfig">Kubeconfig YAML</label>
          <textarea class="text-input" id="cluster-kubeconfig" rows="10" placeholder="apiVersion: v1&#10;clusters:&#10;  - cluster:&#10;      server: https://127.0.0.1:6443&#10;..." required style="width: 100%; font-family: var(--admin-mono); font-size: 11px;"></textarea>
          
          <label class="form-label" for="cluster-context" style="margin-top: 14px;">Cluster Name / Context (optional, e.g. production-us, staging-eu — give each cluster a unique name so both show up in the rail)</label>
          <input class="text-input" id="cluster-context" placeholder="e.g. minikube, kind-crelan, arn:aws:eks:..." style="width: 100%;">
          
          <div id="cluster-connect-state" style="margin: 14px 0;"></div>
          <button class="admin-btn admin-btn-primary" id="cluster-connect-submit" type="submit" style="padding: 9px 18px; font-size: 12px;">Validate and Connect Cluster</button>
        </form>
      </div>

      <!-- Tab 2: ServiceAccount Token -->
      <div id="tab-content-token" style="display: none;">
        <p style="color: var(--admin-text-muted); font-size: 13px; margin-top: 0; line-height: 1.6;">
          Connect directly via the Kubernetes API server endpoint and a long-lived ServiceAccount Bearer Token. Ideal for avoiding local cert file references and expired cloud CLI plugins.
        </p>

        <div style="background: rgba(15, 23, 42, 0.6); border: 1px solid var(--admin-border); border-radius: 6px; padding: 12px 14px; margin-bottom: 16px;">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px;">
            <span style="font-size: 12px; font-weight: 600; color: #38bdf8;">1-Click Setup: Generate credentials in your cluster</span>
            <button class="admin-btn admin-btn-xs" id="btn-copy-sa-cmd" type="button">Copy Script</button>
          </div>
          <pre id="sa-setup-script" style="font-family: var(--admin-mono); font-size: 11px; margin: 0; color: #cbd5e1; overflow-x: auto; white-space: pre-wrap;">kubectl create serviceaccount crelan-admin -n default && kubectl create clusterrolebinding crelan-admin-binding --clusterrole=cluster-admin --serviceaccount=default:crelan-admin && kubectl create token crelan-admin -n default --duration=8760h</pre>
        </div>

        <form id="admin-token-connect-form">
          <label class="form-label" for="token-api-url">Kubernetes API Server URL *</label>
          <input class="text-input" id="token-api-url" placeholder="https://10.0.0.1:6443 or https://k8s.yourdomain.com:6443" required style="width: 100%;">

          <label class="form-label" for="token-bearer-token" style="margin-top: 14px;">ServiceAccount Bearer Token *</label>
          <textarea class="text-input" id="token-bearer-token" rows="4" placeholder="eyJhbGciOiJSUzI1NiIs..." required style="width: 100%; font-family: var(--admin-mono); font-size: 11px;"></textarea>

          <label class="form-label" for="token-cluster-name" style="margin-top: 14px;">Context / Cluster Name (optional)</label>
          <input class="text-input" id="token-cluster-name" placeholder="e.g. production-k8s, staging-eks" style="width: 100%;">

          <label class="form-label" for="token-ca-cert" style="margin-top: 14px;">CA Certificate (optional PEM string or base64)</label>
          <textarea class="text-input" id="token-ca-cert" rows="2" placeholder="-----BEGIN CERTIFICATE-----&#10;... (Leave empty to use cluster default or skip verify)" style="width: 100%; font-family: var(--admin-mono); font-size: 11px;"></textarea>

          <div style="margin-top: 12px; display: flex; align-items: center; gap: 8px;">
            <input type="checkbox" id="token-insecure-verify" checked>
            <label for="token-insecure-verify" style="font-size: 12px; cursor: pointer;">Skip TLS Certificate Verification (recommended for internal/self-signed IP endpoints)</label>
          </div>

          <div id="token-connect-state" style="margin: 14px 0;"></div>
          <button class="admin-btn admin-btn-primary" id="token-connect-submit" type="submit" style="padding: 9px 18px; font-size: 12px;">Validate and Connect with Token</button>
        </form>
      </div>

      <!-- Tab 3: In-Cluster Agent (Tunnel) -->
      <div id="tab-content-agent" style="display: none;">
        <p style="color: var(--admin-text-muted); font-size: 13px; margin-top: 0; line-height: 1.6;">
          Ideal for <strong>private VPCs, firewalled clusters, or local clusters (k3s / minikube)</strong> without an inbound public IP or VPN. The agent dials outbound to Crelan.
        </p>

        <div id="agent-manifest-loading" style="padding: 12px; color: var(--admin-accent); font-size: 12px;">Loading agent configuration…</div>
        <div id="agent-manifest-view" style="display: none;">
          <div style="background: rgba(15, 23, 42, 0.6); border: 1px solid var(--admin-border); border-radius: 6px; padding: 14px; margin-bottom: 16px;">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
              <strong style="font-size: 12px; color: #38bdf8;">1-Line Install Command (Run in your cluster terminal)</strong>
              <button class="admin-btn admin-btn-xs" id="btn-copy-agent-cmd" type="button">Copy Command</button>
            </div>
            <code id="agent-install-cmd" style="display: block; font-family: var(--admin-mono); font-size: 11px; color: #f8fafc; background: rgba(0,0,0,0.3); padding: 8px 12px; border-radius: 4px; overflow-x: auto; word-break: break-all;"></code>
          </div>

          <details style="margin-top: 12px;">
            <summary style="font-size: 12px; color: var(--admin-accent); cursor: pointer; font-weight: 600;">View Raw Kubernetes YAML Manifest</summary>
            <div style="position: relative; margin-top: 8px;">
              <button class="admin-btn admin-btn-xs" id="btn-copy-agent-yaml" style="position: absolute; top: 8px; right: 8px;" type="button">Copy YAML</button>
              <pre id="agent-raw-yaml" style="font-family: var(--admin-mono); font-size: 10px; max-height: 250px; overflow-y: auto; background: var(--admin-surface-muted, #0f172a); border: 1px solid var(--admin-border); border-radius: 6px; padding: 12px; color: #cbd5e1;"></pre>
            </div>
          </details>
        </div>
      </div>
    </div>`;

  // Tab switching logic
  const tabKube = $("#tab-btn-kubeconfig");
  const tabToken = $("#tab-btn-token");
  const tabAgent = $("#tab-btn-agent");
  const viewKube = $("#tab-content-kubeconfig");
  const viewToken = $("#tab-content-token");
  const viewAgent = $("#tab-content-agent");

  function switchTab(activeBtn, activeView) {
    [tabKube, tabToken, tabAgent].forEach(b => {
      b.classList.remove("admin-btn-primary");
    });
    activeBtn.classList.add("admin-btn-primary");
    [viewKube, viewToken, viewAgent].forEach(v => {
      v.style.display = "none";
    });
    activeView.style.display = "block";
  }

  tabKube.addEventListener("click", () => switchTab(tabKube, viewKube));
  tabToken.addEventListener("click", () => switchTab(tabToken, viewToken));
  tabAgent.addEventListener("click", async () => {
    switchTab(tabAgent, viewAgent);
    const loading = $("#agent-manifest-loading");
    const view = $("#agent-manifest-view");
    if (view.dataset.loaded) return;
    try {
      const data = await request("/api/v1/admin/clusters/agent-manifest");
      $("#agent-install-cmd").textContent = data.install_command;
      $("#agent-raw-yaml").textContent = data.manifest_yaml;
      loading.style.display = "none";
      view.style.display = "block";
      view.dataset.loaded = "true";

      $("#btn-copy-agent-cmd").onclick = () => {
        navigator.clipboard.writeText(data.install_command);
        $("#btn-copy-agent-cmd").textContent = "Copied!";
        setTimeout(() => { $("#btn-copy-agent-cmd").textContent = "Copy Command"; }, 2000);
      };
      $("#btn-copy-agent-yaml").onclick = () => {
        navigator.clipboard.writeText(data.manifest_yaml);
        $("#btn-copy-agent-yaml").textContent = "Copied!";
        setTimeout(() => { $("#btn-copy-agent-yaml").textContent = "Copy YAML"; }, 2000);
      };
    } catch (err) {
      loading.textContent = "Could not load agent manifest: " + err.message;
    }
  });

  // Copy helper for SA script
  const copySaBtn = $("#btn-copy-sa-cmd");
  if (copySaBtn) {
    copySaBtn.onclick = () => {
      navigator.clipboard.writeText($("#sa-setup-script").textContent.trim());
      copySaBtn.textContent = "Copied!";
      setTimeout(() => { copySaBtn.textContent = "Copy Script"; }, 2000);
    };
  }

  // Handle Tab 1 (Kubeconfig) Submission
  $("#admin-connect-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    await handleClusterConnectSubmission({
      method: "kubeconfig",
      kubeconfig: $("#cluster-kubeconfig").value,
      context: $("#cluster-context").value.trim(),
    }, $("#cluster-connect-submit"), $("#cluster-connect-state"));
  });

  // Handle Tab 2 (Token) Submission
  $("#admin-token-connect-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    await handleClusterConnectSubmission({
      method: "token",
      api_server_url: $("#token-api-url").value.trim(),
      bearer_token: $("#token-bearer-token").value.trim(),
      context: $("#token-cluster-name").value.trim(),
      ca_cert: $("#token-ca-cert").value.trim(),
      insecure_skip_verify: $("#token-insecure-verify").checked,
    }, $("#token-connect-submit"), $("#token-connect-state"));
  });
}

async function handleClusterConnectSubmission(payload, btn, st) {
  btn.disabled = true;
  st.innerHTML = `<p style="color: var(--admin-accent); font-weight: 600; font-size: 12px; margin: 8px 0;">Connecting to Kubernetes API server and verifying access…</p>`;
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 15000);
  try {
    const res = await fetch("/api/v1/admin/clusters/connect", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      signal: controller.signal,
      body: JSON.stringify(payload),
    });
    clearTimeout(timeoutId);
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || ("Connection failed (" + res.status + ")"));
    const warningHtml = data.persist_warning
      ? `<p style="font-size: 11px; margin: 6px 0 0; color: var(--admin-amber);">${escapeHTML(data.persist_warning)}</p>`
      : "";
    st.innerHTML = `
      <div style="padding: 14px 16px; background: var(--admin-green-bg); border: 1px solid var(--admin-green-border); border-radius: 6px; color: var(--admin-green); margin: 12px 0;">
        <strong style="display: block; font-size: 13px; margin-bottom: 2px;">Connected successfully to “${escapeHTML(data.context)}”!</strong>
        <span style="font-size: 12px;">The cluster is active and ready for platform administration and workloads.</span>
        ${warningHtml}
      </div>
      <div style="display: flex; gap: 8px; margin-top: 12px;">
        <a class="admin-btn admin-btn-primary" href="#pods">View Pods</a>
        <a class="admin-btn" href="#nodes">View Nodes</a>
        <a class="admin-btn" href="#overview">Go to Overview</a>
      </div>
    `;
    await loadClusterHeader();
    await loadNamespacesDropdown();
    await loadClusterFleetRail();
    if (state.activeView === "cluster-fleet") {
      await renderClusterFleet($("#admin-content-area"));
    }
  } catch (err) {
    clearTimeout(timeoutId);
    const isTimeout = err.name === "AbortError" || (err.message && err.message.toLowerCase().includes("timeout"));
    const errMsg = isTimeout
      ? "Connection attempt timed out after 15 seconds. Ensure the Kubernetes API server URL is reachable from this server and any network/firewall allows access."
      : err.message;
    st.innerHTML = `
      <div style="padding: 14px 16px; background: var(--admin-red-bg); border: 1px solid var(--admin-red-border); border-radius: 6px; color: var(--admin-red); margin: 12px 0;">
        <strong style="display: block; font-size: 13px; margin-bottom: 2px;">Connection Failed</strong>
        <span style="font-size: 12px;">${escapeHTML(errMsg)}</span>
      </div>
    `;
  } finally {
    clearTimeout(timeoutId);
    btn.disabled = false;
  }
}

// -------------------------------------------------------------
// Granular Cluster & Resource Access Control UI
// -------------------------------------------------------------

async function renderAccessControl(container) {
  container.innerHTML = `<div class="admin-loading-state">Loading team access & permissions…</div>`;
  try {
    const data = await request("/api/v1/admin/access/grants");
    const grants = data.grants || [];
    const members = data.members || [];

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Cluster & Resource Access Control</h1>
        </div>
        <div style="display: flex; gap: 8px;">
          <button class="admin-btn admin-btn-primary" id="btn-toggle-grant-form">+ Grant Permission</button>
        </div>
      </div>

      <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; margin-bottom: 20px;">
        <div style="background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 8px; padding: 16px;">
          <span style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Active Granular Grants</span>
          <div style="font-size: 24px; font-weight: 700; margin-top: 4px; color: var(--admin-accent);">${grants.length}</div>
        </div>
        <div style="background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 8px; padding: 16px;">
          <span style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Workspace Members</span>
          <div style="font-size: 24px; font-weight: 700; margin-top: 4px; color: var(--admin-text);">${members.length}</div>
        </div>
        <div style="background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 8px; padding: 16px;">
          <span style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Resource Scopes</span>
          <div style="font-size: 24px; font-weight: 700; margin-top: 4px; color: var(--admin-green);">6 Types</div>
          <div style="font-size: 11px; color: var(--admin-text-muted); margin-top: 2px;">Cluster, Deployment, Database, Service, Storage, Settings</div>
        </div>
      </div>

      <!-- Add Grant Form Card (Collapsible) -->
      <div id="grant-form-card" style="display: none; background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 8px; padding: 20px; margin-bottom: 20px;">
        <h3 style="margin: 0 0 8px 0; font-size: 14px;">Grant Resource / Cluster Permission</h3>
        <p style="font-size: 12px; color: var(--admin-text-muted); margin: 0 0 16px 0;">
          Assign read, write, or admin authority to a team member on specific clusters or components.
        </p>
        <form id="create-grant-form">
          <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 14px;">
            <div>
              <label class="form-label" for="grant-user-id">Target Member *</label>
              <select class="text-input" id="grant-user-id" required style="width: 100%;">
                ${members.map(m => `<option value="${m.user_id}">${escapeHTML(m.name || m.email)} (${m.role})</option>`).join("")}
              </select>
            </div>
            <div>
              <label class="form-label" for="grant-resource-type">Resource Scope *</label>
              <select class="text-input" id="grant-resource-type" required style="width: 100%;">
                <option value="cluster">Cluster</option>
                <option value="deployment">Deployment</option>
                <option value="database">Database</option>
                <option value="service">Service</option>
                <option value="storage">Storage</option>
                <option value="settings">Settings</option>
              </select>
            </div>
            <div>
              <label class="form-label" for="grant-resource-id">Cluster / Resource Name</label>
              <input class="text-input" id="grant-resource-id" placeholder="e.g. minikube, prod-cluster, or *" value="*" style="width: 100%;">
              <span style="font-size: 10px; color: var(--admin-text-muted);">Use '*' for all resources of this type</span>
            </div>
            <div>
              <label class="form-label" for="grant-permission">Permission Level *</label>
              <select class="text-input" id="grant-permission" required style="width: 100%;">
                <option value="read">Read-Only (Viewer)</option>
                <option value="write" selected>Read & Write (Developer / Operator)</option>
                <option value="admin">Full Administrative Control (Admin)</option>
              </select>
            </div>
          </div>
          <div style="display: flex; gap: 8px; margin-top: 16px; align-items: center;">
            <button class="admin-btn admin-btn-primary" type="submit">Save Access Grant</button>
            <button class="admin-btn" type="button" id="btn-cancel-grant-form">Cancel</button>
            <span id="grant-form-msg" style="font-size: 12px; margin-left: 10px;"></span>
          </div>
        </form>
      </div>

      <!-- Live Access Tester -->
      <div style="background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 8px; padding: 18px; margin-bottom: 20px;">
        <h3 style="margin: 0 0 6px 0; font-size: 13px; font-weight: 600;">Live Access Tester & Validator</h3>
        <p style="font-size: 12px; color: var(--admin-text-muted); margin: 0 0 14px 0;">
          Test whether a user is authorized to perform an action on a specific cluster or resource in real time.
        </p>
        <div style="display: flex; gap: 10px; flex-wrap: wrap; align-items: flex-end;">
          <div style="flex: 1; min-width: 160px;">
            <label class="form-label" style="font-size: 11px;">User</label>
            <select class="text-input" id="check-user-select" style="width: 100%; padding: 6px 10px; font-size: 12px;">
              ${members.map(m => `<option value="${m.user_id}">${escapeHTML(m.name || m.email)} (${m.role})</option>`).join("")}
            </select>
          </div>
          <div style="flex: 1; min-width: 130px;">
            <label class="form-label" style="font-size: 11px;">Resource Type</label>
            <select class="text-input" id="check-resource-type" style="width: 100%; padding: 6px 10px; font-size: 12px;">
              <option value="cluster">Cluster</option>
              <option value="deployment">Deployment</option>
              <option value="database">Database</option>
              <option value="service">Service</option>
              <option value="storage">Storage</option>
              <option value="settings">Settings</option>
            </select>
          </div>
          <div style="flex: 1; min-width: 140px;">
            <label class="form-label" style="font-size: 11px;">Target Scope / Name</label>
            <input class="text-input" id="check-resource-id" placeholder="e.g. minikube or *" value="*" style="width: 100%; padding: 6px 10px; font-size: 12px;">
          </div>
          <div style="flex: 1; min-width: 120px;">
            <label class="form-label" style="font-size: 11px;">Required Action</label>
            <select class="text-input" id="check-perm-verb" style="width: 100%; padding: 6px 10px; font-size: 12px;">
              <option value="read">read (inspect)</option>
              <option value="write">write (mutate/deploy)</option>
              <option value="admin">admin (manage)</option>
            </select>
          </div>
          <button class="admin-btn admin-btn-primary" id="btn-run-access-check" style="height: 33px; font-size: 12px;">Run Test</button>
        </div>
        <div id="check-result-box" style="margin-top: 12px; display: none;"></div>
      </div>

      <!-- Active Grants Table -->
      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Member</th>
              <th>Resource Scope</th>
              <th>Target / Cluster Name</th>
              <th>Permission Level</th>
              <th>Granted At</th>
              <th style="text-align: right;">Action</th>
            </tr>
          </thead>
          <tbody>
            ${grants.length === 0 ? `
              <tr><td colspan="6" style="text-align: center; color: var(--admin-text-muted); padding: 24px;">No granular access grants configured. Workspace baseline roles (Owner, Developer, Viewer) apply across all clusters.</td></tr>
            ` : grants.map(g => `
              <tr>
                <td class="admin-table-name">
                  <strong>${escapeHTML(g.user_name || "User #" + g.user_id)}</strong>
                  <div style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(g.user_email)}</div>
                </td>
                <td><span class="admin-badge" style="font-family: var(--admin-mono);">${escapeHTML(g.resource_type)}</span></td>
                <td><code style="font-family: var(--admin-mono); font-size: 11px; background: rgba(255,255,255,0.06); padding: 2px 6px; border-radius: 4px;">${escapeHTML(g.resource_id)}</code></td>
                <td>
                  <span class="admin-badge ${g.permission === 'admin' ? 'admin-badge-danger' : g.permission === 'write' ? 'admin-badge-warning' : 'admin-badge-ready'}">
                    ${escapeHTML(g.permission.toUpperCase())}
                  </span>
                </td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${g.created_at ? escapeHTML(g.created_at.slice(0, 19).replace("T", " ")) : "-"}</td>
                <td style="text-align: right;">
                  <button class="admin-btn admin-btn-xs admin-btn-danger" onclick="revokeAccessGrant(${g.id})">Revoke</button>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;

    // Toggle Grant Form
    const grantCard = $("#grant-form-card");
    $("#btn-toggle-grant-form").onclick = () => {
      grantCard.style.display = grantCard.style.display === "none" ? "block" : "none";
    };
    $("#btn-cancel-grant-form").onclick = () => {
      grantCard.style.display = "none";
    };

    // Handle Create Grant Submission
    $("#create-grant-form").onsubmit = async (e) => {
      e.preventDefault();
      const msg = $("#grant-form-msg");
      msg.style.color = "var(--admin-accent)";
      msg.textContent = "Saving grant…";
      try {
        const res = await fetch("/api/v1/admin/access/grants", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            user_id: parseInt($("#grant-user-id").value, 10),
            resource_type: $("#grant-resource-type").value,
            resource_id: $("#grant-resource-id").value.trim(),
            permission: $("#grant-permission").value,
          }),
        });
        const respData = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(respData.error || ("Failed (" + res.status + ")"));
        msg.style.color = "var(--admin-green)";
        msg.textContent = "Saved successfully!";
        setTimeout(() => renderAccessControl(container), 800);
      } catch (err) {
        msg.style.color = "var(--admin-red)";
        msg.textContent = err.message;
      }
    };

    // Live Access Tester
    $("#btn-run-access-check").onclick = async () => {
      const resultBox = $("#check-result-box");
      resultBox.style.display = "block";
      resultBox.innerHTML = `<span style="font-size: 12px; color: var(--admin-accent);">Evaluating access policy…</span>`;
      try {
        const checkRes = await fetch("/api/v1/admin/access/check", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            user_id: parseInt($("#check-user-select").value, 10),
            resource_type: $("#check-resource-type").value,
            resource_id: $("#check-resource-id").value.trim(),
            required_permission: $("#check-perm-verb").value,
          }),
        });
        const evalData = await checkRes.json().catch(() => ({}));
        if (!checkRes.ok) throw new Error(evalData.error || "Evaluation failed");
        resultBox.innerHTML = `
          <div style="padding: 10px 14px; border-radius: 6px; font-size: 12px; background: ${evalData.allowed ? "var(--admin-green-bg)" : "var(--admin-red-bg)"}; border: 1px solid ${evalData.allowed ? "var(--admin-green-border)" : "var(--admin-red-border)"}; color: ${evalData.allowed ? "var(--admin-green)" : "var(--admin-red)"};">
            <strong>${evalData.allowed ? "ACCESS PERMITTED" : "ACCESS DENIED"}</strong>: ${escapeHTML(evalData.reason)}
          </div>
        `;
      } catch (err) {
        resultBox.innerHTML = `<div style="color: var(--admin-red); font-size: 12px;">Error evaluating access: ${escapeHTML(err.message)}</div>`;
      }
    };

  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to load access control: ${escapeHTML(err.message)}</div>`;
  }
}

window.revokeAccessGrant = async function(grantID) {
  if (!confirm("Are you sure you want to revoke this access grant?")) return;
  try {
    const res = await fetch("/api/v1/admin/access/grants/" + grantID, {
      method: "DELETE",
      credentials: "same-origin",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || "Revoke failed");
    renderAccessControl($("#admin-content-area"));
  } catch (err) {
    alert("Could not revoke access grant: " + err.message);
  }
};

async function renderReadiness(container) {
  const readiness = await request("/api/v1/admin/readiness");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Cluster Readiness Checklist</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderReadiness($('#admin-content-area'))">Re-check</button>
    </div>
    <div style="margin-bottom: 16px; padding: 12px 16px; border-radius: 6px; background: ${readiness.ready ? "var(--admin-green-bg)" : "var(--admin-amber-bg)"}; border: 1px solid ${readiness.ready ? "var(--admin-green-border)" : "var(--admin-amber-border)"};">
      <strong style="color: ${readiness.ready ? "var(--admin-green)" : "var(--admin-amber)"};">
        ${readiness.ready ? "All core cluster prerequisites verified." : "Some cluster components need attention before offering all managed database engines."}
      </strong>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Check Name</th><th>Status</th><th>Details</th></tr></thead>
        <tbody>
          ${readiness.checks.map((c) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td><span class="admin-badge ${c.ready ? "admin-badge-ready" : "admin-badge-warning"}">${c.ready ? "Ready" : "Needs Attention"}</span></td>
              <td style="color: var(--admin-text-muted); font-size: 12px;">${escapeHTML(c.detail)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function renderBackup(container) {
  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Control-Plane Backup</h1></div></div>
    <div style="max-width: 600px; background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 9px; padding: 24px;">
      <h3 style="margin-top: 0; font-family: var(--admin-font-heading);">SQLite Control-Plane Database Snapshot</h3>
      <p style="color: var(--admin-text-muted); font-size: 12px;">
        Download a consistent point-in-time snapshot of the Crelan control-plane SQLite database (including workspaces, deployments, users, and audit records).
      </p>
      <button class="admin-btn admin-btn-primary" id="btn-do-backup" type="button" style="margin-top: 12px;">
        Download Control-Plane Snapshot (.db)
      </button>
      <p id="backup-state-text" style="color: var(--admin-text-dim); font-size: 11px; margin-top: 10px;"></p>
    </div>`;

  $("#btn-do-backup").addEventListener("click", async () => {
    const btn = $("#btn-do-backup");
    const st = $("#backup-state-text");
    btn.disabled = true;
    st.textContent = "Creating database snapshot…";
    try {
      const res = await fetch("/api/v1/admin/control-plane-backup", { method: "POST", credentials: "same-origin" });
      if (!res.ok) throw new Error("Backup failed");
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = "crelan-control-plane.db";
      link.click();
      URL.revokeObjectURL(url);
      st.textContent = "Snapshot downloaded successfully.";
    } catch (e) {
      st.textContent = e.message;
    } finally {
      btn.disabled = false;
    }
  });
}

// -------------------------------------------------------------
// Slide-Over Resource Inspector
// -------------------------------------------------------------

async function openInspector(kind, namespace, name) {
  state.activeInspector = { kind, namespace, name };
  $("#drawer-kind").textContent = kind;
  $("#drawer-title").textContent = name;

  const toolbar = $("#drawer-toolbar");
  toolbar.replaceChildren();

  if (kind === "pod") {
    const btnDiag = createBtn("Diagnose", "", () => openAIDiagnosis(namespace, name));
    const btnLogs = createBtn("Logs", "", () => openPodLogs(namespace, name));
    const btnExec = createBtn("Terminal", "", () => openPodTerminal(namespace, name));
    const btnDebug = createBtn("Debug Shell", "", () => openEphemeralDebugModal(namespace, name));
    const btnDel = createBtn("Delete", "admin-btn-danger", () => confirmDeletePod(namespace, name));
    toolbar.append(btnDiag, btnLogs, btnExec, btnDebug, btnDel);
  } else if (kind === "deployment") {
    const btnRestart = createBtn("Rollout Restart", "", () => triggerRolloutRestart(namespace, name));
    const btnScale = createBtn("Scale", "", () => openScaleModal(namespace, name, 1));
    const btnDel = createBtn("Delete", "admin-btn-danger", () => confirmDeleteDeployment(namespace, name));
    toolbar.append(btnRestart, btnScale, btnDel);
  } else if (kind === "pvc" || kind === "persistentvolumeclaim") {
    const btnYaml = createBtn("View YAML", "", () => {
      $$(".admin-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "yaml"));
      renderInspectorTab("yaml");
    });
    const btnDel = createBtn("Delete PVC", "admin-btn-danger", () => {
      closeInspector();
      deletePVC(namespace, name);
    });
    toolbar.append(btnYaml, btnDel);
  } else if (kind === "pv" || kind === "persistentvolume") {
    const btnYaml = createBtn("View YAML", "", () => {
      $$(".admin-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "yaml"));
      renderInspectorTab("yaml");
    });
    toolbar.append(btnYaml);
  } else if (kind === "node") {
    const btnDrain = createBtn("Drain Sim", "", () => openNodeDrainModal(name));
    const btnYaml = createBtn("View YAML", "", () => {
      $$(".admin-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "yaml"));
      renderInspectorTab("yaml");
    });
    toolbar.append(btnDrain, btnYaml);
  }

  $$(".admin-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "overview"));

  $("#admin-drawer-overlay").classList.add("open");
  $("#admin-inspector-drawer").classList.add("open");

  await renderInspectorTab("overview");
}

function createBtn(text, extraClass, handler) {
  const b = document.createElement("button");
  b.className = `admin-btn admin-btn-sm ${extraClass}`;
  b.textContent = text;
  b.onclick = handler;
  return b;
}

function closeInspector() {
  $("#admin-drawer-overlay").classList.remove("open");
  $("#admin-inspector-drawer").classList.remove("open");
  state.activeInspector = null;
}

async function renderInspectorTab(tab) {
  if (!state.activeInspector) return;
  const { kind, namespace, name } = state.activeInspector;
  const body = $("#drawer-body");
  body.innerHTML = `<div class="admin-empty-state">Loading ${tab}…</div>`;

  try {
    if (tab === "overview") {
      let raw;
      if (kind === "pod") raw = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else if (kind === "deployment") raw = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else if (kind === "node") raw = await request(`/api/v1/admin/nodes/${encodeURIComponent(name)}`);
      else if (kind === "secret") raw = await request(`/api/v1/admin/secrets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else raw = await request(`/api/v1/admin/resource/yaml?kind=${encodeURIComponent(kind)}&namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`);

      state.activeInspector.raw = raw;
      renderInspectorOverview(body, kind, raw);
    } else if (tab === "events") {
      const events = await request(`/api/v1/admin/events?namespace=${encodeURIComponent(namespace)}`);
      const scoped = events.filter((e) => e.object_name === name);
      body.innerHTML = `
        <div class="admin-table-container">
          <table class="admin-table">
            <thead><tr><th>Type</th><th>Reason</th><th>Message</th><th>Age</th></tr></thead>
            <tbody>
              ${scoped.length === 0 ? `<tr><td colspan="4" class="admin-empty-state">No events recorded for this resource.</td></tr>` : ""}
              ${scoped.map((e) => `
                <tr>
                  <td><span class="admin-badge ${e.type === "Warning" ? "admin-badge-warning" : "admin-badge-ready"}">${escapeHTML(e.type)}</span></td>
                  <td><strong>${escapeHTML(e.reason)}</strong></td>
                  <td>${escapeHTML(e.message)}</td>
                  <td>${escapeHTML(e.age)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>`;
    } else if (tab === "yaml") {
      const raw = state.activeInspector.raw || await request(`/api/v1/admin/resource/yaml?kind=${encodeURIComponent(kind)}&namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`);
      const jsonStr = JSON.stringify(raw, null, 2);
      body.innerHTML = `
        <div class="admin-yaml-wrap">
          <button class="admin-yaml-copy-btn" id="btn-copy-yaml">Copy JSON</button>
          <pre class="admin-yaml-code">${escapeHTML(jsonStr)}</pre>
        </div>`;
      $("#btn-copy-yaml").onclick = () => {
        navigator.clipboard.writeText(jsonStr);
        $("#btn-copy-yaml").textContent = "Copied!";
        setTimeout(() => ($("#btn-copy-yaml").textContent = "Copy JSON"), 2000);
      };
    }
  } catch (err) {
    body.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Error: ${escapeHTML(err.message)}</div>`;
  }
}

function renderInspectorOverview(container, kind, raw) {
  const meta = raw.metadata || {};
  let specDetails = "";

  if (kind === "secret" && raw.data) {
    specDetails = `
      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Decoded Secret Data (${raw.keys} keys)</div>
        ${Object.entries(raw.data).map(([k, v]) => `
          <div class="admin-spec-row">
            <span class="admin-spec-label">${escapeHTML(k)}</span>
            <span class="admin-spec-val" style="color: var(--admin-amber);">${escapeHTML(v)}</span>
          </div>
        `).join("")}
      </div>`;
  } else if (kind === "pvc" || kind === "persistentvolumeclaim") {
    const spec = raw.spec || {};
    const status = raw.status || {};
    const cap = (status.capacity && status.capacity.storage) || (spec.resources && spec.resources.requests && spec.resources.requests.storage) || "-";
    specDetails = `
      <div class="admin-spec-group">
        <div class="admin-spec-group-title">PersistentVolumeClaim Status & Specs</div>
        <div class="admin-spec-row"><span class="admin-spec-label">Phase Status</span><span class="admin-spec-val"><span class="admin-badge ${status.phase === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(status.phase || "Unknown")}</span></span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Bound Volume</span><span class="admin-spec-val"><code>${escapeHTML(spec.volumeName || "-")}</code></span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Storage Capacity</span><span class="admin-spec-val">${escapeHTML(cap)}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">StorageClass</span><span class="admin-spec-val">${escapeHTML(spec.storageClassName || "-")}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Access Modes</span><span class="admin-spec-val">${escapeHTML((spec.accessModes || []).join(", ") || "-")}</span></div>
      </div>`;
  } else if (kind === "pv" || kind === "persistentvolume") {
    const spec = raw.spec || {};
    const status = raw.status || {};
    const cap = (spec.capacity && spec.capacity.storage) || "-";
    specDetails = `
      <div class="admin-spec-group">
        <div class="admin-spec-group-title">PersistentVolume Status & Specs</div>
        <div class="admin-spec-row"><span class="admin-spec-label">Phase Status</span><span class="admin-spec-val"><span class="admin-badge ${status.phase === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(status.phase || "Unknown")}</span></span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Storage Capacity</span><span class="admin-spec-val">${escapeHTML(cap)}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Reclaim Policy</span><span class="admin-spec-val">${escapeHTML(spec.persistentVolumeReclaimPolicy || "-")}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">StorageClass</span><span class="admin-spec-val">${escapeHTML(spec.storageClassName || "-")}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Access Modes</span><span class="admin-spec-val">${escapeHTML((spec.accessModes || []).join(", ") || "-")}</span></div>
        <div class="admin-spec-row"><span class="admin-spec-label">Claim Reference</span><span class="admin-spec-val">${spec.claimRef ? `${escapeHTML(spec.claimRef.namespace)}/${escapeHTML(spec.claimRef.name)}` : "None"}</span></div>
      </div>`;
  }

  container.innerHTML = `
    <div class="admin-spec-group">
      <div class="admin-spec-group-title">Resource Metadata</div>
      <div class="admin-spec-row"><span class="admin-spec-label">Name</span><span class="admin-spec-val">${escapeHTML(meta.name || raw.name)}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">Namespace</span><span class="admin-spec-val">${escapeHTML(meta.namespace || raw.namespace || "-")}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">UID</span><span class="admin-spec-val">${escapeHTML(meta.uid || "-")}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">Created</span><span class="admin-spec-val">${escapeHTML(meta.creationTimestamp || raw.created_at || "-")}</span></div>
    </div>
    ${specDetails}
    <div class="admin-spec-group">
      <div class="admin-spec-group-title">Labels</div>
      ${meta.labels ? Object.entries(meta.labels).map(([k, v]) => `
        <div class="admin-spec-row"><span class="admin-spec-label">${escapeHTML(k)}</span><span class="admin-spec-val">${escapeHTML(v)}</span></div>
      `).join("") : `<p style="color: var(--admin-text-dim); font-size: 11px;">None</p>`}
    </div>
  `;
}

// -------------------------------------------------------------
// Interactive In-Browser Pod Exec
// -------------------------------------------------------------

function openPodTerminal(namespace, podName, container = "") {
  state.activeTerminal = { pod: podName, namespace, container };
  $("#terminal-pod-name").textContent = podName;
  $("#terminal-container-name").textContent = container || "default";
  $("#terminal-prompt").textContent = `[${escapeHTML(podName)}]$`;
  $("#admin-terminal-drawer").classList.add("open");
  $("#terminal-input").focus();
}

function closePodTerminal() {
  $("#admin-terminal-drawer").classList.remove("open");
  state.activeTerminal = null;
}

async function runTerminalCommand(cmd) {
  if (!state.activeTerminal || !cmd.trim()) return;
  const out = $("#terminal-output");
  const { pod, namespace, container } = state.activeTerminal;

  out.textContent += `\n[sh]$ ${cmd}\nRunning...\n`;
  $("#terminal-body").scrollTop = $("#terminal-body").scrollHeight;

  try {
    const res = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/exec`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ command: cmd, container }),
    });

    let result = "";
    if (res.stdout) result += res.stdout;
    if (res.stderr) result += `\n[STDERR] ` + res.stderr;
    if (res.error) result += `\n[EXEC ERROR] ` + res.error;

    out.textContent += result + "\n";
  } catch (err) {
    out.textContent += `Error: ${err.message}\n`;
  }
  $("#terminal-body").scrollTop = $("#terminal-body").scrollHeight;
}

// -------------------------------------------------------------
// Pod Logs Viewer
// -------------------------------------------------------------

async function openPodLogs(namespace, podName) {
  state.activeLogs = { pod: podName, namespace, container: "" };
  $("#logs-pod-name").textContent = `${namespace}/${podName}`;
  $("#admin-logs-drawer").classList.add("open");

  try {
    const pod = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}`);
    const select = $("#logs-container-select");
    select.replaceChildren();
    if (pod.spec && pod.spec.containers) {
      pod.spec.containers.forEach((c) => {
        const opt = document.createElement("option");
        opt.value = c.name;
        opt.textContent = c.name;
        select.append(opt);
      });
    }
  } catch (e) {}

  await fetchAndDisplayLogs();
}

function closePodLogs() {
  $("#admin-logs-drawer").classList.remove("open");
  if (state.activeLogs && state.activeLogs.timer) {
    clearInterval(state.activeLogs.timer);
  }
  state.activeLogs = null;
}

async function fetchAndDisplayLogs() {
  if (!state.activeLogs) return;
  const { pod, namespace } = state.activeLogs;
  const container = $("#logs-container-select").value || "";
  const tail = $("#logs-tail-select").value || "250";
  const prev = $("#logs-previous-check").checked;

  try {
    const logs = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/logs?container=${encodeURIComponent(container)}&tail=${tail}&previous=${prev}`);
    const filter = ($("#logs-filter-input").value || "").toLowerCase();
    const out = $("#logs-output");

    if (filter) {
      const lines = logs.split("\n").filter((l) => l.toLowerCase().includes(filter));
      out.textContent = lines.join("\n");
    } else {
      out.textContent = logs || "No container log entries found.";
    }

    if ($("#logs-autoscroll-check").checked) {
      $("#logs-body").scrollTop = $("#logs-body").scrollHeight;
    }
  } catch (err) {
    $("#logs-output").textContent = `Error fetching logs: ${err.message}`;
  }
}

// -------------------------------------------------------------
// AI Diagnostic Copilot
// -------------------------------------------------------------

async function openAIDiagnosis(namespace, podName) {
  const modal = $("#admin-ai-modal");
  const body = $("#ai-modal-body");
  modal.classList.add("open");
  body.innerHTML = `<div class="admin-empty-state">Running root-cause diagnosis for ${escapeHTML(podName)}…</div>`;

  try {
    const diag = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/diagnose`);

    let exitCodeHtml = "";
    if (diag.exit_code !== undefined && diag.exit_code !== 0) {
      exitCodeHtml = `<span class="admin-badge admin-badge-crashloop">Exit Code ${escapeHTML(diag.exit_code)}${diag.last_termination_reason ? ` (${escapeHTML(diag.last_termination_reason)})` : ""}</span>`;
    }

    body.innerHTML = `
      <div class="admin-ai-banner">
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 6px;">
          <div class="admin-ai-banner-title" style="margin: 0;">
            ${escapeHTML(diag.summary)}
          </div>
          ${exitCodeHtml}
        </div>
        <div style="font-size: 11px; color: var(--admin-text-muted); margin-bottom: 8px;">Confidence: <strong>${escapeHTML(diag.confidence)}</strong> · Severity: <strong>${escapeHTML(diag.severity.toUpperCase())}</strong></div>
        <div style="display: flex; gap: 8px; margin-top: 8px;">
          <button class="admin-btn admin-btn-primary admin-btn-sm" type="button" onclick="$('#admin-ai-modal').classList.remove('open'); openPostMortem('${escapeHTML(namespace)}', '${escapeHTML(podName)}')">Generate Post-Mortem & Runbook</button>
          <a class="admin-btn admin-btn-sm" href="#war-room?ns=${encodeURIComponent(namespace)}&pod=${encodeURIComponent(podName)}" onclick="$('#admin-ai-modal').classList.remove('open')">Incident War Room</a>
        </div>
      </div>

      ${diag.log_anomaly ? `
        <div class="admin-ai-anomaly-box" style="background: rgba(220, 38, 38, 0.08); border-left: 3px solid var(--admin-red); padding: 10px 14px; margin-bottom: 14px; border-radius: 4px;">
          <div style="font-size: 11px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Detected Log Anomaly / Error</div>
          <div style="font-family: monospace; font-size: 12px; color: var(--admin-red); word-break: break-all;">${escapeHTML(diag.log_anomaly)}</div>
        </div>
      ` : ""}

      <div class="admin-ai-cause-box">
        <div class="admin-ai-cause-title">Probable Cause & Root Analysis</div>
        <p style="margin: 0; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(diag.probable_cause)}</p>
      </div>

      ${diag.stack_trace ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title" style="display: flex; justify-content: space-between; align-items: center;">
            <span>Extracted Application Stack Trace</span>
            <span style="font-size: 10px; font-weight: normal; color: var(--admin-text-muted);">Automated RCA</span>
          </div>
          <pre class="admin-yaml-code" style="max-height: 220px; color: #dc2626; background: #fff5f5; border: 1px solid #fecaca;">${escapeHTML(diag.stack_trace)}</pre>
        </div>
      ` : ""}

      ${diag.recommended_steps && diag.recommended_steps.length > 0 ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title">Recommended Action Plan</div>
          <ol style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text); line-height: 1.7;">
            ${diag.recommended_steps.map((st) => `<li>${escapeHTML(st)}</li>`).join("")}
          </ol>
        </div>
      ` : ""}

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Diagnostic Evidence</div>
        <ul style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text-muted);">
          ${diag.evidence.map((ev) => `<li style="margin-bottom: 4px;">${escapeHTML(ev)}</li>`).join("")}
        </ul>
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Remediation Actions</div>
        ${diag.remediation.map((act) => `
          <div class="admin-remediation-card">
            <div class="admin-remediation-info">
              <strong>${escapeHTML(act.title)}</strong>
              <span>${escapeHTML(act.description)}</span>
            </div>
            ${act.auto_fixable ? `<button class="admin-btn admin-btn-primary admin-btn-sm" onclick="runAutoFix('${escapeHTML(act.action_id)}', '${escapeHTML(namespace)}', '${escapeHTML(podName)}')">Apply Fix</button>` : ""}
          </div>
        `).join("")}
      </div>

      ${diag.relevant_logs ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title">Recent Crash Logs</div>
          <pre class="admin-yaml-code" style="max-height: 200px;">${escapeHTML(diag.relevant_logs)}</pre>
        </div>` : ""}
    `;
  } catch (err) {
    body.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Diagnosis failed: ${escapeHTML(err.message)}</div>`;
  }
}

async function runAutoFix(actionId, namespace, podName) {
  try {
    if (actionId === "restart_pod") {
      await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}?force=true`, { method: "DELETE" });
      alert(`Pod ${podName} evicted. Controller is restarting container.`);
    } else if (actionId === "restart_deployment") {
      const parts = podName.split("-");
      parts.pop();
      parts.pop();
      const depName = parts.join("-");
      await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(depName)}/restart`, { method: "POST" });
      alert(`Rollout restart triggered for deployment ${depName}.`);
    }
    $("#admin-ai-modal").classList.remove("open");
    renderCurrentView();
  } catch (err) {
    alert("Fix execution failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Fast Remediation Actions (Restart, Scale, Delete)
// -------------------------------------------------------------

async function triggerRolloutRestart(namespace, deploymentName) {
  if (!confirm(`Trigger a rolling restart for deployment “${deploymentName}”?`)) return;
  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/restart`, { method: "POST" });
    alert(`Rollout restart initiated for ${deploymentName}.`);
    renderCurrentView();
  } catch (err) {
    alert("Restart failed: " + err.message);
  }
}

function openScaleModal(namespace, name, currentReplicas) {
  $("#scale-dep-name").textContent = `${namespace}/${name}`;
  $("#scale-range").value = currentReplicas || 1;
  $("#scale-input").value = currentReplicas || 1;
  $("#scale-modal-form").dataset.ns = namespace;
  $("#scale-modal-form").dataset.name = name;
  $("#admin-scale-modal").classList.add("open");
}

function openCreateNamespaceModal() {
  $("#new-ns-name").value = "";
  $("#admin-create-ns-modal").classList.add("open");
  $("#new-ns-name").focus();
}

async function confirmDeletePod(namespace, podName) {
  if (!confirm(`Delete pod “${podName}”? (Kubernetes will reschedule if managed by deployment)`)) return;
  try {
    await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}`, { method: "DELETE" });
    closeInspector();
    renderCurrentView();
  } catch (err) {
    alert("Pod deletion failed: " + err.message);
  }
}

async function confirmDeleteDeployment(namespace, depName) {
  if (!confirm(`Are you sure you want to permanently delete deployment “${depName}”?`)) return;
  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(depName)}`, { method: "DELETE" });
    closeInspector();
    renderCurrentView();
  } catch (err) {
    alert("Deployment deletion failed: " + err.message);
  }
}

async function deletePVC(namespace, name) {
  if (!confirm(`Delete persistent volume claim “${name}”? Any retained disk data will be deleted.`)) return;
  try {
    await request(`/api/v1/admin/pvcs/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`, { method: "DELETE" });
    renderCurrentView();
  } catch (err) {
    alert("Delete failed: " + err.message);
  }
}

async function deleteNamespace(name) {
  if (!confirm(`Permanently delete namespace “${name}” and all workloads within it?`)) return;
  try {
    await request(`/api/v1/admin/namespaces/${encodeURIComponent(name)}`, { method: "DELETE" });
    await loadNamespacesDropdown();
    renderCurrentView();
  } catch (err) {
    alert("Namespace deletion failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Event Listeners & Initialization
// -------------------------------------------------------------

function shouldAutoRefresh() {
  if (state.activeTerminal || state.activeLogs || state.activeInspector) return false;
  if (state.activeView === "cluster-connect" || state.activeView === "backup") return false;
  const tag = (document.activeElement && document.activeElement.tagName) || "";
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return false;
  if (document.querySelector(".admin-modal-overlay.open")) return false;
  return true;
}

function setupEventListeners() {
  window.addEventListener("hashchange", renderCurrentView);

  // Topbar Namespace Select
  $("#admin-namespace-select").addEventListener("change", (e) => {
    state.selectedNamespace = e.target.value;
    renderCurrentView();
  });

  // Topbar Search Filter
  $("#admin-search-input").addEventListener("input", (e) => {
    state.searchQuery = e.target.value.trim();
    renderCurrentView();
  });

  // Hotkey "/" for search
  window.addEventListener("keydown", (e) => {
    if (e.key === "/" && document.activeElement.tagName !== "INPUT" && document.activeElement.tagName !== "TEXTAREA") {
      e.preventDefault();
      $("#admin-search-input").focus();
    }
  });

  // Domain Rail Switchers
  $$(".admin-rail-domain-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      switchDomain(btn.dataset.domain, true);
    });
  });

  // Perspective Toggle
  $("#perspective-k8s").addEventListener("click", () => {
    state.perspective = "k8s";
    $("#perspective-k8s").classList.add("active");
    $("#perspective-ws").classList.remove("active");
    renderCurrentView();
  });

  $("#perspective-ws").addEventListener("click", () => {
    state.perspective = "workspace";
    $("#perspective-ws").classList.add("active");
    $("#perspective-k8s").classList.remove("active");
    renderCurrentView();
  });

  // Refresh Button
  $("#admin-refresh-btn").addEventListener("click", async () => {
    const icon = $("#admin-refresh-icon");
    icon.style.display = "inline-block";
    icon.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 400 });
    await Promise.all([loadClusterHeader(), renderCurrentView()]);
  });

  $("#rail-refresh-btn").addEventListener("click", () => $("#admin-refresh-btn").click());

  // Cluster Rail & Customization Modal
  const railAddBtn = $("#rail-add-cluster-btn");
  if (railAddBtn) {
    railAddBtn.addEventListener("click", () => setRoute("cluster-connect"));
  }
  const modalClose = $("#modal-cluster-close-btn");
  if (modalClose) modalClose.addEventListener("click", closeClusterColorModal);
  const modalCancel = $("#modal-cluster-cancel-btn");
  if (modalCancel) modalCancel.addEventListener("click", closeClusterColorModal);
  const modalSave = $("#modal-cluster-save-btn");
  if (modalSave) modalSave.addEventListener("click", saveClusterCustomization);
  const modalAlias = $("#modal-cluster-alias");
  if (modalAlias) modalAlias.addEventListener("input", updateModalClusterPreview);
  const modalHex = $("#modal-cluster-color-hex");
  if (modalHex) {
    modalHex.addEventListener("input", (e) => {
      const val = e.target.value.trim();
      if (/^#[0-9a-fA-F]{6}$/.test(val)) {
        selectedClusterCustomColor = val;
        $("#modal-cluster-color-native").value = val;
        updateModalClusterPreview();
        renderClickUpColorPalette();
      }
    });
  }
  const modalNat = $("#modal-cluster-color-native");
  if (modalNat) {
    modalNat.addEventListener("input", (e) => {
      selectedClusterCustomColor = e.target.value;
      $("#modal-cluster-color-hex").value = e.target.value;
      updateModalClusterPreview();
      renderClickUpColorPalette();
    });
  }

  // Auto-Refresh
  $("#admin-auto-refresh-select").addEventListener("change", (e) => {
    if (state.autoRefreshTimer) clearInterval(state.autoRefreshTimer);
    const ms = parseInt(e.target.value, 10);
    if (ms > 0) {
      state.autoRefreshTimer = setInterval(() => {
        if (shouldAutoRefresh()) {
          loadClusterHeader();
          renderCurrentView();
        }
      }, ms);
    }
  });

  // Drawer Tabs & Close
  $("#drawer-close-btn").addEventListener("click", closeInspector);
  $("#admin-drawer-overlay").addEventListener("click", closeInspector);
  $$(".admin-drawer-tab").forEach((btn) => {
    btn.addEventListener("click", () => {
      $$(".admin-drawer-tab").forEach((t) => t.classList.remove("active"));
      btn.classList.add("active");
      renderInspectorTab(btn.dataset.tab);
    });
  });

  // Terminal Controls
  $("#terminal-close-btn").addEventListener("click", closePodTerminal);
  $("#terminal-clear-btn").addEventListener("click", () => ($("#terminal-output").textContent = ""));
  $("#terminal-form").addEventListener("submit", (e) => {
    e.preventDefault();
    const inp = $("#terminal-input");
    const cmd = inp.value;
    inp.value = "";
    runTerminalCommand(cmd);
  });
  $$(".admin-quick-cmd-btn").forEach((btn) => {
    btn.addEventListener("click", () => runTerminalCommand(btn.dataset.cmd));
  });

  // Logs Controls
  $("#logs-close-btn").addEventListener("click", closePodLogs);
  $("#logs-container-select").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-tail-select").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-previous-check").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-filter-input").addEventListener("input", fetchAndDisplayLogs);
  $("#logs-download-btn").addEventListener("click", () => {
    const text = $("#logs-output").textContent;
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${state.activeLogs.pod}.log`;
    a.click();
    URL.revokeObjectURL(url);
  });

  // AI Modal Close
  $("#ai-modal-close").addEventListener("click", () => $("#admin-ai-modal").classList.remove("open"));
  $("#ai-modal-dismiss").addEventListener("click", () => $("#admin-ai-modal").classList.remove("open"));

  // Scale Modal Range Sync
  $("#scale-range").addEventListener("input", (e) => ($("#scale-input").value = e.target.value));
  $("#scale-input").addEventListener("input", (e) => ($("#scale-range").value = e.target.value));
  $("#scale-modal-cancel").addEventListener("click", () => $("#admin-scale-modal").classList.remove("open"));
  $("#scale-modal-close").addEventListener("click", () => $("#admin-scale-modal").classList.remove("open"));
  $("#scale-modal-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const ns = e.target.dataset.ns;
    const name = e.target.dataset.name;
    const reps = parseInt($("#scale-input").value, 10);
    try {
      await request(`/api/v1/admin/deployments/${encodeURIComponent(ns)}/${encodeURIComponent(name)}/scale`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ replicas: reps }),
      });
      $("#admin-scale-modal").classList.remove("open");
      renderCurrentView();
    } catch (err) {
      alert("Scale failed: " + err.message);
    }
  });

  // Create Namespace Modal
  $("#create-ns-close").addEventListener("click", () => $("#admin-create-ns-modal").classList.remove("open"));
  $("#create-ns-cancel").addEventListener("click", () => $("#admin-create-ns-modal").classList.remove("open"));
  $("#create-ns-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = $("#new-ns-name").value.trim();
    try {
      await request("/api/v1/admin/namespaces", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      $("#admin-create-ns-modal").classList.remove("open");
      await loadNamespacesDropdown();
      renderCurrentView();
    } catch (err) {
      alert("Namespace creation failed: " + err.message);
    }
  });

  // Right-Sizing Modal
  $("#rightsize-modal-close").addEventListener("click", () => $("#admin-rightsize-modal").classList.remove("open"));
  $("#rightsize-modal-cancel").addEventListener("click", () => $("#admin-rightsize-modal").classList.remove("open"));
  $("#rightsize-modal-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const modal = $("#admin-rightsize-modal");
    const ns = modal.dataset.namespace;
    const name = modal.dataset.deployment;
    const cpuReq = $("#rightsize-cpu-req").value.trim();
    const cpuLim = $("#rightsize-cpu-lim").value.trim();
    const memReq = $("#rightsize-mem-req").value.trim();
    const memLim = $("#rightsize-mem-lim").value.trim();
    try {
      const res = await request(`/api/v1/admin/deployments/${encodeURIComponent(ns)}/${encodeURIComponent(name)}/rightsize`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          cpu_request: cpuReq,
          cpu_limit: cpuLim,
          mem_request: memReq,
          mem_limit: memLim,
        }),
      });
      modal.classList.remove("open");
      alert(res.message || "Resources successfully updated.");
      if (state.activeView === "rightsizing") {
        renderRightSizing($("#admin-content-area"));
      } else {
        renderCurrentView();
      }
    } catch (err) {
      alert("Right-sizing failed: " + err.message);
    }
  });

  // Copilot Modal & Keyboard Shortcut
  function openQuickCopilot(prompt = "") {
    const modal = $("#admin-copilot-modal");
    modal.classList.add("open");
    const inp = $("#copilot-modal-input");
    inp.focus();
    if (prompt) {
      inp.value = prompt;
      runQuickCopilot(prompt);
    }
  }

  async function runQuickCopilot(q) {
    if (!q || !q.trim()) return;
    const results = $("#copilot-modal-results");
    results.innerHTML = `<div class="admin-empty-state">Analyzing cluster with AI Copilot…</div>`;
    try {
      const resp = await request("/api/v1/admin/ai/copilot", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: q }),
      });
      let matchesHtml = "";
      if (resp.matches && resp.matches.length > 0) {
        matchesHtml = `
          <div style="margin-top: 12px; max-height: 200px; overflow-y: auto;">
            <table class="admin-table">
              <thead><tr><th>Kind</th><th>Name</th><th>Status</th><th>Action</th></tr></thead>
              <tbody>
                ${resp.matches.slice(0, 8).map((m) => `
                  <tr>
                    <td><span class="admin-kind-badge">${escapeHTML(m.kind)}</span></td>
                    <td class="admin-table-name"><strong>${escapeHTML(m.name)}</strong></td>
                    <td><span class="admin-badge ${m.status === "CrashLoopBackOff" || m.status === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(m.status)}</span></td>
                    <td>
                      ${m.link ? `<a class="admin-btn admin-btn-sm" href="${escapeHTML(m.link)}" onclick="$('#admin-copilot-modal').classList.remove('open')">View</a>` : ""}
                    </td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          </div>
        `;
      }
      results.innerHTML = `
        <div class="admin-ai-banner" style="margin-top: 8px;">
          <div style="font-size: 11px; font-weight: 700; color: var(--admin-accent); margin-bottom: 4px;">Intent: ${escapeHTML(resp.intent)}</div>
          <p style="margin: 0; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(resp.summary)}</p>
        </div>
        ${matchesHtml}
      `;
    } catch (err) {
      results.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Copilot error: ${escapeHTML(err.message)}</div>`;
    }
  }

  $("#topbar-copilot-btn").addEventListener("click", () => openQuickCopilot());
  $("#copilot-modal-close").addEventListener("click", () => $("#admin-copilot-modal").classList.remove("open"));
  $("#copilot-modal-dismiss").addEventListener("click", () => $("#admin-copilot-modal").classList.remove("open"));
  $("#copilot-modal-form").addEventListener("submit", (e) => {
    e.preventDefault();
    runQuickCopilot($("#copilot-modal-input").value);
  });
  $$("#copilot-quick-prompts button").forEach((btn) => {
    btn.addEventListener("click", () => {
      $("#copilot-modal-input").value = btn.dataset.prompt;
      runQuickCopilot(btn.dataset.prompt);
    });
  });

  window.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      openCommandPalette();
    } else if (e.key === "Escape") {
      closeCommandPalette();
      closeInspector();
      closeClusterColorModal();
    }
  });

  // Post-Mortem Modal
  $("#postmortem-modal-close")?.addEventListener("click", () => $("#admin-postmortem-modal").classList.remove("open"));
  $("#postmortem-modal-dismiss")?.addEventListener("click", () => $("#admin-postmortem-modal").classList.remove("open"));
  $("#postmortem-modal-copy-btn")?.addEventListener("click", copyPostMortemMarkdown);
  $("#btn-copy-postmortem-markdown")?.addEventListener("click", copyPostMortemMarkdown);
  $("#btn-download-postmortem-md")?.addEventListener("click", downloadPostMortemMD);
  $("#btn-download-postmortem-json")?.addEventListener("click", downloadPostMortemJSON);
  $("#btn-download-postmortem-html")?.addEventListener("click", downloadPostMortemHTML);

  $("#tab-btn-postmortem-preview")?.addEventListener("click", () => switchPostMortemTab("preview"));
  $("#tab-btn-postmortem-markdown")?.addEventListener("click", () => switchPostMortemTab("markdown"));
  $("#tab-btn-postmortem-runbook")?.addEventListener("click", () => switchPostMortemTab("runbook"));

  // Rollback Modal
  $("#rollback-modal-close")?.addEventListener("click", () => $("#admin-rollback-modal").classList.remove("open"));
  $("#rollback-modal-dismiss")?.addEventListener("click", () => $("#admin-rollback-modal").classList.remove("open"));

  // Drain Simulator Modal
  $("#drain-modal-close")?.addEventListener("click", () => $("#admin-drain-modal").classList.remove("open"));
  $("#drain-modal-dismiss")?.addEventListener("click", () => $("#admin-drain-modal").classList.remove("open"));

  // Webhook Modal
  $("#webhook-modal-close")?.addEventListener("click", () => $("#admin-webhook-modal").classList.remove("open"));
  $("#webhook-modal-dismiss")?.addEventListener("click", () => $("#admin-webhook-modal").classList.remove("open"));
  $("#webhook-form")?.addEventListener("submit", handleCreateWebhook);

  // Ephemeral Debug Modal
  $("#debug-modal-close")?.addEventListener("click", closeEphemeralDebugModal);
  $("#debug-modal-dismiss")?.addEventListener("click", closeEphemeralDebugModal);
  $("#debug-image-select")?.addEventListener("change", (e) => {
    const customWrap = $("#debug-custom-image-wrap");
    if (e.target.value === "custom") {
      customWrap.hidden = false;
      $("#debug-custom-image-input").focus();
    } else {
      customWrap.hidden = true;
    }
  });
  $("#debug-form")?.addEventListener("submit", handleLaunchDebugContainer);

  // Enterprise Suite Event Listeners
  $("#open-cmd-k-btn")?.addEventListener("click", () => openCommandPalette());
  $("#cmd-k-close-btn")?.addEventListener("click", () => closeCommandPalette());
  $("#admin-cmd-k-modal")?.addEventListener("click", (e) => {
    if (e.target.id === "admin-cmd-k-modal") closeCommandPalette();
  });
  $("#cmd-k-input")?.addEventListener("input", (e) => filterCommandPalette(e.target.value));
  $("#cmd-k-input")?.addEventListener("keydown", handleCommandPaletteKeydown);

  $("#helm-modal-close")?.addEventListener("click", () => $("#admin-helm-modal").classList.remove("open"));
  $("#tab-btn-helm-user")?.addEventListener("click", () => switchHelmValuesTab("user"));
  $("#tab-btn-helm-computed")?.addEventListener("click", () => switchHelmValuesTab("computed"));
  $("#helm-modal-copy-btn")?.addEventListener("click", copyHelmValuesToClipboard);

  $("#cve-modal-close")?.addEventListener("click", () => $("#admin-cve-modal").classList.remove("open"));

  // SRE Modals
  $("#slo-modal-close")?.addEventListener("click", () => $("#admin-slo-modal").classList.remove("open"));
  $("#slo-modal-dismiss")?.addEventListener("click", () => $("#admin-slo-modal").classList.remove("open"));
  $("#admin-slo-modal")?.addEventListener("click", (e) => {
    if (e.target.id === "admin-slo-modal") $("#admin-slo-modal").classList.remove("open");
  });
  $("#slo-form")?.addEventListener("submit", handleCreateSLO);

  $("#chaos-modal-close")?.addEventListener("click", () => $("#admin-chaos-modal").classList.remove("open"));
  $("#chaos-modal-dismiss")?.addEventListener("click", () => $("#admin-chaos-modal").classList.remove("open"));
  $("#admin-chaos-modal")?.addEventListener("click", (e) => {
    if (e.target.id === "admin-chaos-modal") $("#admin-chaos-modal").classList.remove("open");
  });
  $("#chaos-form")?.addEventListener("submit", handleRunChaos);

  // Sign out
  $("#admin-signout").addEventListener("click", async () => {
    await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    window.location.assign("/login");
  });
}

// -------------------------------------------------------------
// Operational Intelligence Feature 1: Incident War Room
// -------------------------------------------------------------

async function renderWarRoom(container) {
  const hash = window.location.hash.split("?")[1] || "";
  const params = new URLSearchParams(hash);
  const ns = params.get("ns") || "";
  const pod = params.get("pod") || "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Incident War Room</h1>
        <p>Live incident triage, root-cause investigation, and shared war room console</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" id="btn-share-warroom" type="button">Copy War Room Link</button>
        <button class="admin-btn admin-btn-primary admin-btn-sm" id="btn-warroom-postmortem" type="button" disabled>Generate AI Post-Mortem</button>
      </div>
    </div>
    <div class="admin-empty-state">Connecting to active incident telemetry…</div>
  `;

  try {
    const url = `/api/v1/admin/incidents/snapshot?namespace=${encodeURIComponent(ns)}&pod=${encodeURIComponent(pod)}`;
    const snapshot = await request(url);

    const diag = snapshot.diagnosis || {};
    let exitBadge = "";
    if (snapshot.exit_code !== undefined && snapshot.exit_code !== 0) {
      exitBadge = `<span class="admin-badge admin-badge-crashloop">Exit ${escapeHTML(snapshot.exit_code)} (${escapeHTML(snapshot.reason || "Terminated")})</span>`;
    }

    let eventsHtml = `<div style="color: var(--admin-text-muted); font-size: 12px; padding: 10px 0;">No warning events recorded for this workload.</div>`;
    if (snapshot.warning_events && snapshot.warning_events.length > 0) {
      eventsHtml = `
        <table class="admin-table admin-timeline-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th style="width: 140px;">Timestamp</th>
              <th style="width: 110px;">Reason</th>
              <th>Message</th>
            </tr>
          </thead>
          <tbody>
            ${snapshot.warning_events.map(ev => `
              <tr>
                <td style="color: var(--admin-text-muted);">${escapeHTML(ev.last_timestamp || ev.first_timestamp || "recently")}</td>
                <td><span class="admin-badge admin-badge-warning">${escapeHTML(ev.reason)}</span></td>
                <td>${escapeHTML(ev.message)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      `;
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span class="admin-badge admin-badge-danger" style="font-size: 11px;">INCIDENT WAR ROOM</span>
            <span style="font-family: var(--admin-mono); font-size: 12px; color: var(--admin-text-muted);">${escapeHTML(snapshot.incident_id)}</span>
          </div>
          <h1>${escapeHTML(snapshot.pod_name)}</h1>
          <p>Target Workload in namespace <strong>${escapeHTML(snapshot.namespace)}</strong> · Captured: ${escapeHTML(snapshot.captured_at)}</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" id="btn-share-warroom" type="button">Copy War Room Link</button>
          <button class="admin-btn admin-btn-primary admin-btn-sm" id="btn-warroom-postmortem" type="button">Generate AI Post-Mortem</button>
        </div>
      </div>

      <!-- Incident KPI Banner -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 16px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Phase & State</span></div>
          <div class="admin-stat-value" style="font-size: 18px; color: var(--admin-red);">${escapeHTML(snapshot.phase)}</div>
          <div class="admin-stat-subtext">${exitBadge || "Non-zero exit"}</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Severity</span></div>
          <div class="admin-stat-value" style="font-size: 18px; text-transform: uppercase;">${escapeHTML(diag.severity || "CRITICAL")}</div>
          <div class="admin-stat-subtext">Confidence: ${escapeHTML(diag.confidence || "High")}</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Warning Events</span></div>
          <div class="admin-stat-value" style="font-size: 18px;">${snapshot.warning_events ? snapshot.warning_events.length : 0}</div>
          <div class="admin-stat-subtext">Cluster controller events</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Quick Remediations</span></div>
          <div style="margin-top: 10px; display: flex; gap: 6px;">
            <button class="admin-btn admin-btn-danger admin-btn-sm" type="button" onclick="confirmDeletePod('${escapeHTML(snapshot.namespace)}', '${escapeHTML(snapshot.pod_name)}')">Evict Pod</button>
            <button class="admin-btn admin-btn-sm" type="button" onclick="openInspector('pod', '${escapeHTML(snapshot.namespace)}', '${escapeHTML(snapshot.pod_name)}')">Inspect Pod</button>
          </div>
        </div>
      </div>

      <!-- Split Layout: RCA & Timeline vs Live Logs -->
      <div class="admin-warroom-split">
        <!-- Left: Diagnosis & Events -->
        <div>
          <div class="admin-ai-banner" style="margin-bottom: 14px;">
            <div class="admin-ai-banner-title">${escapeHTML(diag.summary || "Workload failure detected")}</div>
            <p style="margin: 6px 0 0; font-size: 12px; color: var(--admin-text); line-height: 1.5;">${escapeHTML(diag.probable_cause || "")}</p>
          </div>

          ${diag.log_anomaly ? `
            <div style="background: rgba(220, 38, 38, 0.08); border-left: 3px solid var(--admin-red); padding: 10px 14px; margin-bottom: 14px; border-radius: 4px;">
              <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Extracted Anomaly / Exception</div>
              <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-red); word-break: break-all;">${escapeHTML(diag.log_anomaly)}</div>
            </div>
          ` : ""}

          ${diag.stack_trace ? `
            <div class="admin-spec-group" style="margin-bottom: 14px;">
              <div class="admin-spec-group-title">Application Stack Trace</div>
              <pre class="admin-stacktrace-block">${escapeHTML(diag.stack_trace)}</pre>
            </div>
          ` : ""}

          <div class="admin-spec-group">
            <div class="admin-spec-group-title">Cluster Controller Events Timeline</div>
            <div class="admin-table-container" style="max-height: 260px; overflow-y: auto;">
              ${eventsHtml}
            </div>
          </div>
        </div>

        <!-- Right: Recent Container Logs Console -->
        <div>
          <div class="admin-warroom-console">
            <div class="admin-warroom-console-header">
              <span>Container Logs Tail (Incident Telemetry)</span>
              <button class="admin-btn admin-btn-sm" style="padding: 2px 8px; font-size: 10px;" type="button" onclick="navigator.clipboard.writeText($('#warroom-logs-code').textContent); this.textContent='Copied'; setTimeout(() => this.textContent='Copy Logs', 1800);">Copy Logs</button>
            </div>
            <div class="admin-warroom-console-body" id="warroom-logs-code">${escapeHTML(snapshot.recent_logs || "No logs available for container.")}</div>
          </div>
        </div>
      </div>
    `;

    $("#btn-warroom-postmortem").onclick = () => openPostMortem(snapshot.namespace, snapshot.pod_name);
    $("#btn-share-warroom").onclick = () => {
      const fullUrl = `${window.location.origin}/admin#war-room?ns=${encodeURIComponent(snapshot.namespace)}&pod=${encodeURIComponent(snapshot.pod_name)}`;
      navigator.clipboard.writeText(fullUrl).then(() => {
        $("#btn-share-warroom").textContent = "Link Copied";
        setTimeout(() => { $("#btn-share-warroom").textContent = "Copy War Room Link"; }, 2000);
      });
    };
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Incident War Room</h1></div>
      </div>
      <div class="admin-empty-state">
        <h2 style="font-size: 16px; margin-bottom: 8px;">No Active Workload Incidents Found</h2>
        <p style="color: var(--admin-text-muted); max-width: 460px; margin: 0 auto 16px;">
          All workloads are running smoothly. If you suspect a failure, navigate to <a href="#pods">Pods</a> and click "AI RCA" or "War Room" on any specific workload.
        </p>
        <a class="admin-btn admin-btn-primary" href="#pods">View Cluster Pods</a>
      </div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 2: Platform Audit Trail
// -------------------------------------------------------------

async function renderAuditTrail(container) {
  state.auditNs = state.auditNs || "";
  state.auditAction = state.auditAction || "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Platform Action Audit Trail</h1>
        <p>Immutable forensic record of all administrative operations, rollouts, scaling, and security hardening executed on the platform</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderAuditTrail(document.getElementById('admin-content-area'))">Refresh Trail</button>
      </div>
    </div>
    <div class="admin-filter-bar" style="display: flex; gap: 10px; margin-bottom: 16px; align-items: center;">
      <select class="admin-select" id="audit-ns-select" style="min-width: 180px;">
        <option value="">All Namespaces</option>
        ${state.namespaces.map(ns => `<option value="${escapeHTML(ns)}" ${ns === state.auditNs ? "selected" : ""}>${escapeHTML(ns)}</option>`).join("")}
      </select>
      <select class="admin-select" id="audit-action-select" style="min-width: 160px;">
        <option value="">All Action Types</option>
        <option value="restart" ${state.auditAction === "restart" ? "selected" : ""}>restart</option>
        <option value="scale" ${state.auditAction === "scale" ? "selected" : ""}>scale</option>
        <option value="patch-env" ${state.auditAction === "patch-env" ? "selected" : ""}>patch-env</option>
        <option value="harden" ${state.auditAction === "harden" ? "selected" : ""}>harden</option>
        <option value="rightsize" ${state.auditAction === "rightsize" ? "selected" : ""}>rightsize</option>
        <option value="delete" ${state.auditAction === "delete" ? "selected" : ""}>delete</option>
        <option value="create" ${state.auditAction === "create" ? "selected" : ""}>create</option>
      </select>
      <input class="text-input" id="audit-search-filter" placeholder="Filter audit trail by operator or resource..." style="flex: 1; max-width: 320px;">
    </div>
    <div class="admin-table-container">
      <div class="admin-empty-state">Loading platform audit logs…</div>
    </div>
  `;

  $("#audit-ns-select").onchange = (e) => {
    state.auditNs = e.target.value;
    renderAuditTrail(container);
  };
  $("#audit-action-select").onchange = (e) => {
    state.auditAction = e.target.value;
    renderAuditTrail(container);
  };

  try {
    const logs = await request(`/api/v1/admin/audit-trail?namespace=${encodeURIComponent(state.auditNs)}&action_type=${encodeURIComponent(state.auditAction)}`);

    const tableWrap = container.querySelector(".admin-table-container");
    if (!logs || logs.length === 0) {
      tableWrap.innerHTML = `
        <div class="admin-empty-state">
          <h3>No Audit Records Found</h3>
          <p>No operator actions matching the selected filter criteria have been recorded yet.</p>
        </div>
      `;
      return;
    }

    function renderAuditTable(filteredLogs) {
      tableWrap.innerHTML = `
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 160px;">Timestamp (UTC)</th>
              <th style="width: 180px;">Operator</th>
              <th style="width: 100px;">Action</th>
              <th style="width: 100px;">Resource Kind</th>
              <th style="width: 220px;">Namespace / Name</th>
              <th>Execution Details</th>
            </tr>
          </thead>
          <tbody>
            ${filteredLogs.map(l => `
              <tr>
                <td style="color: var(--admin-text-muted); font-size: 11px;">${escapeHTML(l.created_at)}</td>
                <td style="font-weight: 600;">${escapeHTML(l.operator_email)}</td>
                <td><span class="admin-audit-badge ${escapeHTML(l.action_type)}">${escapeHTML(l.action_type)}</span></td>
                <td><strong>${escapeHTML(l.resource_kind)}</strong></td>
                <td>
                  <span style="font-family: var(--admin-mono); font-size: 12px;">${l.namespace ? `${escapeHTML(l.namespace)}/` : ""}${escapeHTML(l.name)}</span>
                </td>
                <td style="color: var(--admin-text); font-size: 12px;">${escapeHTML(l.details || "—")}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      `;
    }

    renderAuditTable(logs);

    $("#audit-search-filter").oninput = (e) => {
      const q = e.target.value.toLowerCase().trim();
      if (!q) {
        renderAuditTable(logs);
      } else {
        const filtered = logs.filter(l => 
          (l.operator_email && l.operator_email.toLowerCase().includes(q)) ||
          (l.name && l.name.toLowerCase().includes(q)) ||
          (l.namespace && l.namespace.toLowerCase().includes(q)) ||
          (l.details && l.details.toLowerCase().includes(q)) ||
          (l.action_type && l.action_type.toLowerCase().includes(q))
        );
        renderAuditTable(filtered);
      }
    };
  } catch (err) {
    container.querySelector(".admin-table-container").innerHTML = `
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to load audit trail: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 3: Cluster Upgrade Radar
// -------------------------------------------------------------

async function renderUpgradeRadar(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Upgrade Radar & API Deprecation Scanner</h1>
        <p>Continuous compatibility assessment for upcoming Kubernetes releases and breaking API removals</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderUpgradeRadar(document.getElementById('admin-content-area'))">Rescan Cluster</button>
      </div>
    </div>
    <div class="admin-empty-state">Evaluating cluster APIs against enterprise Kubernetes target version…</div>
  `;

  try {
    const radar = await request("/api/v1/admin/cluster/upgrade-radar");

    let dialClass = "admin-radar-score-dial";
    if (radar.upgrade_readiness_score < 70) {
      dialClass += " danger";
    } else if (radar.upgrade_readiness_score < 90) {
      dialClass += " warning";
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Cluster Upgrade Radar & API Deprecation Scanner</h1>
          <p>Continuous compatibility assessment for upcoming Kubernetes releases and breaking API removals</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderUpgradeRadar(document.getElementById('admin-content-area'))">Rescan Cluster</button>
        </div>
      </div>

      <!-- Hero Dial -->
      <div class="admin-radar-hero">
        <div class="${dialClass}">
          ${radar.upgrade_readiness_score}%
        </div>
        <div style="flex: 1;">
          <div style="font-size: 11px; text-transform: uppercase; font-weight: 700; color: var(--admin-text-muted); margin-bottom: 4px;">Upgrade Readiness Score</div>
          <h2 style="margin: 0 0 6px; font-family: var(--admin-font-heading); font-size: 20px;">
            Current: <span style="font-family: var(--admin-mono);">${escapeHTML(radar.current_server_version)}</span> → Target: <span style="font-family: var(--admin-mono); color: var(--admin-accent);">${escapeHTML(radar.target_version)}</span>
          </h2>
          <p style="margin: 0; color: var(--admin-text-muted); font-size: 13px;">
            ${radar.critical_blockers_count > 0 
              ? `<strong style="color: var(--admin-red);">${radar.critical_blockers_count} critical upgrade blocker(s)</strong> must be remediated before initiating control-plane upgrade.` 
              : (radar.deprecated_count > 0 ? `Cluster has ${radar.deprecated_count} deprecated API usage(s) flagged for migration.` : "All workloads comply with target Kubernetes APIs. Zero upgrade blockers detected.")}
          </p>
        </div>
        <div style="display: flex; gap: 14px; text-align: center;">
          <div style="padding: 10px 16px; background: #ffffff; border: 1px solid var(--admin-border); border-radius: 8px;">
            <div style="font-size: 22px; font-weight: 700; color: var(--admin-text);">${radar.deprecated_count}</div>
            <div style="font-size: 10px; text-transform: uppercase; color: var(--admin-text-muted);">Deprecated APIs</div>
          </div>
          <div style="padding: 10px 16px; background: #ffffff; border: 1px solid var(--admin-border); border-radius: 8px;">
            <div style="font-size: 22px; font-weight: 700; color: ${radar.critical_blockers_count > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${radar.critical_blockers_count}</div>
            <div style="font-size: 10px; text-transform: uppercase; color: var(--admin-text-muted);">Critical Blockers</div>
          </div>
        </div>
      </div>

      <!-- Deprecations Table -->
      <div class="admin-table-container">
        <div style="padding: 14px 18px; border-bottom: 1px solid var(--admin-border); background: #ffffff; display: flex; justify-content: space-between; align-items: center;">
          <h3 style="margin: 0; font-size: 14px;">Detected Deprecated APIs & Migration Action Plan</h3>
        </div>
        ${(!radar.findings || radar.findings.length === 0) ? `
          <div class="admin-empty-state">
            <h3 style="color: var(--admin-green);">No Deprecated APIs Detected</h3>
            <p>Your cluster workloads are fully aligned with modern Kubernetes API standards.</p>
          </div>
        ` : `
          <table class="admin-table">
            <thead>
              <tr>
                <th style="width: 100px;">Kind</th>
                <th style="width: 180px;">Resource</th>
                <th style="width: 140px;">Current Version / Field</th>
                <th style="width: 100px;">Removed In</th>
                <th style="width: 140px;">Replacement</th>
                <th style="width: 90px;">Severity</th>
                <th>Remediation Guide</th>
              </tr>
            </thead>
            <tbody>
              ${radar.findings.map(f => `
                <tr>
                  <td><strong>${escapeHTML(f.kind)}</strong></td>
                  <td>
                    <span style="font-family: var(--admin-mono); font-size: 12px;">${f.namespace ? `${escapeHTML(f.namespace)}/` : ""}${escapeHTML(f.name)}</span>
                  </td>
                  <td><span class="admin-badge admin-badge-warning">${escapeHTML(f.current_api_version)}</span></td>
                  <td><span class="admin-badge admin-badge-danger">${escapeHTML(f.removed_in)}</span></td>
                  <td><span style="font-family: var(--admin-mono); font-size: 11px;">${escapeHTML(f.replacement_gvk)}</span></td>
                  <td><span class="admin-badge ${f.severity === "critical" ? "admin-badge-danger" : "admin-badge-warning"}">${escapeHTML(f.severity.toUpperCase())}</span></td>
                  <td style="font-size: 12px; color: var(--admin-text);">${escapeHTML(f.migration_steps)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        `}
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Cluster Upgrade Radar</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to run upgrade radar: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 4: Visual Traffic Topology
// -------------------------------------------------------------

async function renderNetworkTopology(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Visual Traffic & Ingress Routing Topology</h1>
        <p>End-to-end traffic routing mapping from public Ingress domains through Services to healthy Pod endpoints</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderNetworkTopology(document.getElementById('admin-content-area'))">Refresh Topology</button>
      </div>
    </div>
    <div class="admin-empty-state">Mapping network traffic routes…</div>
  `;

  try {
    const topo = await request("/api/v1/admin/network/topology");

    let orphanAlertsHtml = "";
    if (topo.orphan_alerts && topo.orphan_alerts.length > 0) {
      orphanAlertsHtml = `
        <div style="background: rgba(220, 38, 38, 0.08); border-left: 4px solid var(--admin-red); padding: 14px 18px; margin-bottom: 20px; border-radius: 6px;">
          <div style="font-weight: 700; color: var(--admin-red); margin-bottom: 6px; font-size: 13px;">Orphan Service Warning: Traffic Black Hole Detected</div>
          <ul style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text);">
            ${topo.orphan_alerts.map(a => `
              <li style="margin-bottom: 4px;">
                <strong>${escapeHTML(a.namespace)}/${escapeHTML(a.service_name)}</strong>: ${escapeHTML(a.reason)} (${escapeHTML(a.remediation)})
              </li>
            `).join("")}
          </ul>
        </div>
      `;
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Visual Traffic & Ingress Routing Topology</h1>
          <p>End-to-end traffic routing mapping from public Ingress domains through Services to healthy Pod endpoints</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderNetworkTopology(document.getElementById('admin-content-area'))">Refresh Topology</button>
        </div>
      </div>

      <!-- KPI Summary -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Ingress Gateways</span></div>
          <div class="admin-stat-value">${topo.total_ingresses || 0}</div>
          <div class="admin-stat-subtext">Active ingress rules</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Cluster Services</span></div>
          <div class="admin-stat-value">${topo.total_services || 0}</div>
          <div class="admin-stat-subtext">ClusterIP & LoadBalancer</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Live Pod Endpoints</span></div>
          <div class="admin-stat-value">${topo.total_endpoints || 0}</div>
          <div class="admin-stat-subtext">Target backend replicas</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Orphan Services</span></div>
          <div class="admin-stat-value" style="color: ${topo.orphan_services_count > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${topo.orphan_services_count || 0}</div>
          <div class="admin-stat-subtext">${topo.orphan_services_count > 0 ? "0 matching backend pods" : "All services routed"}</div>
        </div>
      </div>

      ${orphanAlertsHtml}

      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px;">
        <h3 style="margin: 0; font-size: 15px;">Live Traffic Pipelines</h3>
        <input class="text-input" id="topology-search" placeholder="Filter routes by domain, path, or service..." style="width: 280px; font-size: 12px;">
      </div>

      <div id="topology-cards-container">
        ${(!topo.routes || topo.routes.length === 0) ? `
          <div class="admin-empty-state">No ingress traffic routes detected in cluster.</div>
        ` : topo.routes.map(r => `
          <div class="admin-topology-card" data-search="${escapeHTML(r.host + ' ' + r.path + ' ' + r.service_name).toLowerCase()}">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px;">
              <span style="font-size: 11px; font-weight: 700; color: var(--admin-text-muted); text-transform: uppercase;">
                Namespace: <strong style="color: var(--admin-text);">${escapeHTML(r.namespace)}</strong>
              </span>
              <span class="admin-badge ${r.tls ? "admin-badge-success" : "admin-badge-warning"}">
                ${r.tls ? "TLS / HTTPS" : "HTTP (Insecure)"}
              </span>
            </div>
            <div class="admin-topology-flow">
              <!-- Ingress Node -->
              <div class="admin-topology-node ingress">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #2563eb;">Ingress Gateway</div>
                <div style="font-family: var(--admin-mono); font-weight: 700; font-size: 13px; color: var(--admin-text); margin: 2px 0;">${escapeHTML(r.host)}</div>
                <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-text-muted);">Path: ${escapeHTML(r.path)} (${escapeHTML(r.path_type)})</div>
              </div>

              <div class="admin-topology-arrow">→</div>

              <!-- Service Node -->
              <div class="admin-topology-node service">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #0d9488;">Kubernetes Service</div>
                <div style="font-family: var(--admin-mono); font-weight: 700; font-size: 13px; color: var(--admin-text); margin: 2px 0;">${escapeHTML(r.service_name || "—")}</div>
                <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-text-muted);">Port: ${escapeHTML(r.service_port)} · ${escapeHTML(r.service_type)}</div>
              </div>

              <div class="admin-topology-arrow">→</div>

              <!-- Target Pod Endpoints -->
              <div class="admin-topology-node pod" style="flex: 1; min-width: 240px;">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #16a34a; margin-bottom: 4px;">Target Pod Endpoints (${r.target_pods ? r.target_pods.length : 0})</div>
                ${(!r.target_pods || r.target_pods.length === 0) ? `
                  <div style="color: var(--admin-red); font-size: 11px; font-weight: 600;">No running pods match this service selector</div>
                ` : `
                  <div style="display: flex; flex-wrap: wrap; gap: 6px;">
                    ${r.target_pods.map(p => `
                      <span class="admin-tag-chip" style="cursor: pointer; display: inline-flex; align-items: center; gap: 4px;" onclick="openInspector('pod', '${escapeHTML(r.namespace)}', '${escapeHTML(p.pod_name)}')">
                        <span style="display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: ${p.ready ? "var(--admin-green)" : "var(--admin-red);"}"></span>
                        ${escapeHTML(p.pod_name)} (${escapeHTML(p.pod_ip || "pending")})
                      </span>
                    `).join("")}
                  </div>
                `}
              </div>
            </div>
          </div>
        `).join("")}
      </div>
    `;

    $("#topology-search").oninput = (e) => {
      const q = e.target.value.toLowerCase().trim();
      $$("#topology-cards-container .admin-topology-card").forEach(card => {
        if (!q || card.dataset.search.includes(q)) {
          card.style.display = "";
        } else {
          card.style.display = "none";
        }
      });
    };
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Visual Traffic Topology</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to map network topology: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 5: Tenant Isolation Matrix
// -------------------------------------------------------------

async function renderTenantIsolationMatrix(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Multi-Tenant Network Firewall & Isolation Matrix</h1>
        <p>Capsule tenant boundary checks and NetworkPolicy zero-trust enforcement across namespaces</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderTenantIsolationMatrix(document.getElementById('admin-content-area'))">Rescan Matrix</button>
      </div>
    </div>
    <div class="admin-empty-state">Analyzing cross-namespace network boundary enforcement…</div>
  `;

  try {
    const matrix = await request("/api/v1/admin/network/matrix");

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Multi-Tenant Network Firewall & Isolation Matrix</h1>
          <p>Capsule tenant boundary checks and NetworkPolicy zero-trust enforcement across namespaces</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderTenantIsolationMatrix(document.getElementById('admin-content-area'))">Rescan Matrix</button>
        </div>
      </div>

      <!-- KPI Summary -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Cluster Isolation Score</span></div>
          <div class="admin-stat-value" style="color: ${matrix.isolation_score >= 80 ? "var(--admin-green)" : (matrix.isolation_score >= 50 ? "var(--admin-amber)" : "var(--admin-red);")}">${matrix.isolation_score || 0}%</div>
          <div class="admin-stat-subtext">Zero-trust network posture</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Fully Isolated Tenants</span></div>
          <div class="admin-stat-value" style="color: var(--admin-green);">${matrix.fully_isolated_tenants || 0}</div>
          <div class="admin-stat-subtext">Default-deny ingress & egress</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Partially Isolated</span></div>
          <div class="admin-stat-value" style="color: var(--admin-amber);">${matrix.partially_isolated || 0}</div>
          <div class="admin-stat-subtext">Partial policy coverage</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Unisolated Tenants</span></div>
          <div class="admin-stat-value" style="color: ${matrix.unisolated_tenants > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${matrix.unisolated_tenants || 0}</div>
          <div class="admin-stat-subtext">Open East-West traffic</div>
        </div>
      </div>

      <!-- Matrix Table -->
      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 160px;">Namespace</th>
              <th style="width: 150px;">Capsule Tenant</th>
              <th style="width: 130px;">Default Deny Ingress</th>
              <th style="width: 130px;">Default Deny Egress</th>
              <th>Cross-Namespace Peer Allowlist</th>
              <th style="width: 90px;">Policies</th>
              <th style="width: 150px;">Isolation Status</th>
            </tr>
          </thead>
          <tbody>
            ${(!matrix.tenants || matrix.tenants.length === 0) ? `
              <tr><td colspan="7" class="admin-empty-state">No tenant namespaces found.</td></tr>
            ` : matrix.tenants.map(t => `
              <tr>
                <td><strong>${escapeHTML(t.namespace)}</strong></td>
                <td><span class="admin-tag-chip">${escapeHTML(t.tenant_name)}</span></td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_ingress ? "admin-badge-success" : "admin-badge-danger"}">
                    ${t.has_default_deny_ingress ? "Enforced" : "Open"}
                  </span>
                </td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_egress ? "admin-badge-success" : "admin-badge-warning"}">
                    ${t.has_default_deny_egress ? "Enforced" : "Open"}
                  </span>
                </td>
                <td style="font-family: var(--admin-mono); font-size: 11px;">
                  ${t.allowed_cross_namespaces && t.allowed_cross_namespaces.length > 0 
                    ? t.allowed_cross_namespaces.map(peer => `<span class="admin-tag-chip" style="margin-right: 4px;">${escapeHTML(peer)}</span>`).join("") 
                    : `<span style="color: var(--admin-text-muted);">Strictly Blocked</span>`}
                </td>
                <td><strong>${t.policy_count || 0}</strong></td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_ingress && t.has_default_deny_egress ? "admin-badge-success" : (t.has_default_deny_ingress ? "admin-badge-warning" : "admin-badge-danger")}">
                    ${escapeHTML(t.isolation_status)}
                  </span>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Multi-Tenant Network Firewall</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to calculate isolation matrix: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 6: AI Post-Mortem Modal Logic
// -------------------------------------------------------------

async function openPostMortem(namespace, podName) {
  const modal = $("#admin-postmortem-modal");
  modal.classList.add("open");
  switchPostMortemTab("preview");

  $("#postmortem-preview-body").innerHTML = `<div class="admin-empty-state">Synthesizing AI Post-Mortem & Preventative Runbook for ${escapeHTML(podName)}…</div>`;
  $("#postmortem-markdown-textarea").value = "";
  $("#postmortem-runbook-body").innerHTML = "";

  try {
    const report = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/post-mortem`);
    state.activePostMortem = report;

    $("#postmortem-modal-title").textContent = report.title || "Incident Post-Mortem";
    $("#postmortem-modal-severity").textContent = report.severity || "CRITICAL";
    $("#postmortem-modal-id").textContent = report.incident_id || "INC-POSTMORTEM";
    $("#postmortem-markdown-textarea").value = report.markdown_content || "";

    // Tab 1: Preview
    $("#postmortem-preview-body").innerHTML = `
      <div class="admin-ai-banner">
        <div class="admin-ai-banner-title">Executive Summary</div>
        <p style="margin: 0; font-size: 13px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(report.executive_summary)}</p>
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Incident Chronological Timeline</div>
        <table class="admin-table admin-timeline-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th style="width: 160px;">Timestamp (UTC)</th>
              <th style="width: 140px;">Source</th>
              <th>Event Description</th>
            </tr>
          </thead>
          <tbody>
            ${(report.timeline || []).map(t => `
              <tr>
                <td style="color: var(--admin-text-muted); font-family: var(--admin-mono);">${escapeHTML(t.timestamp)}</td>
                <td><strong>${escapeHTML(t.source)}</strong></td>
                <td>${escapeHTML(t.event)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>

      <div class="admin-ai-cause-box">
        <div class="admin-ai-cause-title">Technical Root Cause Analysis</div>
        <p style="margin: 0 0 8px; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(report.root_cause_technical)}</p>
        ${report.stack_trace ? `
          <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Captured Stack Trace</div>
          <pre class="admin-stacktrace-block">${escapeHTML(report.stack_trace)}</pre>
        ` : ""}
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Multi-Tenant Impact & Blast Radius</div>
        <p style="font-size: 12px; color: var(--admin-text); margin: 0;">${escapeHTML(report.blast_radius)}</p>
      </div>
    `;

    // Tab 3: Preventative Runbook
    $("#postmortem-runbook-body").innerHTML = `
      <div style="margin-bottom: 14px; font-size: 13px; color: var(--admin-text-muted);">Action items synthesized to prevent workload recurrence:</div>
      <div style="display: flex; flex-direction: column; gap: 8px;">
        ${(report.preventative_runbook || []).map((step, idx) => `
          <div style="display: flex; align-items: flex-start; gap: 10px; padding: 10px 14px; background: #f8fafc; border: 1px solid var(--admin-border); border-radius: 6px;">
            <input type="checkbox" id="runbook-chk-${idx}" style="margin-top: 3px;">
            <label for="runbook-chk-${idx}" style="font-size: 12px; color: var(--admin-text); cursor: pointer; flex: 1;">${escapeHTML(step)}</label>
          </div>
        `).join("")}
      </div>
    `;
  } catch (err) {
    $("#postmortem-preview-body").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to generate post-mortem: ${escapeHTML(err.message)}</div>`;
  }
}

function switchPostMortemTab(tab) {
  $("#tab-btn-postmortem-preview")?.classList.toggle("active", tab === "preview");
  $("#tab-btn-postmortem-markdown")?.classList.toggle("active", tab === "markdown");
  $("#tab-btn-postmortem-runbook")?.classList.toggle("active", tab === "runbook");

  $("#postmortem-tab-preview").hidden = (tab !== "preview");
  $("#postmortem-tab-markdown").hidden = (tab !== "markdown");
  $("#postmortem-tab-runbook").hidden = (tab !== "runbook");
}

function copyPostMortemMarkdown() {
  const text = $("#postmortem-markdown-textarea").value;
  if (!text) return;
  navigator.clipboard.writeText(text).then(() => {
    const btn1 = $("#btn-copy-postmortem-markdown");
    const btn2 = $("#postmortem-modal-copy-btn");
    if (btn1) btn1.textContent = "Copied to Clipboard!";
    if (btn2) btn2.textContent = "Copied to Clipboard!";
    setTimeout(() => {
      if (btn1) btn1.textContent = "Copy Markdown to Clipboard";
      if (btn2) btn2.textContent = "1-Click Copy Markdown";
    }, 2000);
  });
}

function triggerFileDownload(filename, content, mimeType) {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function downloadPostMortemMD() {
  const report = state.activePostMortem;
  if (!report || !report.markdown_content) {
    alert("Post-mortem report is not loaded yet.");
    return;
  }
  const filename = `${report.incident_id || 'postmortem'}.md`;
  triggerFileDownload(filename, report.markdown_content, "text/markdown;charset=utf-8");
}

function downloadPostMortemJSON() {
  const report = state.activePostMortem;
  if (!report) {
    alert("Post-mortem report is not loaded yet.");
    return;
  }
  const filename = `${report.incident_id || 'postmortem'}.json`;
  triggerFileDownload(filename, JSON.stringify(report, null, 2), "application/json;charset=utf-8");
}

function downloadPostMortemHTML() {
  const report = state.activePostMortem;
  if (!report) {
    alert("Post-mortem report is not loaded yet.");
    return;
  }
  const filename = `${report.incident_id || 'postmortem'}.html`;
  const htmlContent = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>${escapeHTML(report.title || 'Incident Post-Mortem')}</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #1e293b; background: #fff; margin: 40px auto; max-width: 900px; line-height: 1.6; padding: 0 20px; }
    h1 { font-size: 24px; border-bottom: 2px solid #e2e8f0; padding-bottom: 12px; margin-bottom: 20px; }
    .badge { display: inline-block; padding: 4px 10px; border-radius: 4px; font-weight: 700; font-size: 12px; text-transform: uppercase; background: #fee2e2; color: #991b1b; }
    .meta { font-size: 13px; color: #64748b; margin-bottom: 24px; }
    .section { margin-bottom: 28px; }
    .section-title { font-size: 16px; font-weight: 700; color: #0f172a; margin-bottom: 10px; border-left: 4px solid #2563eb; padding-left: 8px; }
    table { width: 100%; border-collapse: collapse; margin-top: 10px; font-size: 13px; }
    th, td { border: 1px solid #e2e8f0; padding: 8px 12px; text-align: left; }
    th { background: #f8fafc; font-weight: 600; }
    pre { background: #f1f5f9; padding: 12px; border-radius: 6px; overflow-x: auto; font-family: monospace; font-size: 12px; }
    .checklist-item { padding: 8px 0; border-bottom: 1px solid #f1f5f9; font-size: 13px; }
    @media print { body { margin: 20px; max-width: 100%; } }
  </style>
</head>
<body>
  <div style="display: flex; justify-content: space-between; align-items: center;">
    <h1>${escapeHTML(report.title || 'Incident Post-Mortem')}</h1>
    <span class="badge">${escapeHTML(report.severity || 'CRITICAL')}</span>
  </div>
  <div class="meta">
    Incident ID: <strong>${escapeHTML(report.incident_id || '')}</strong> | Generated: <strong>${new Date().toUTCString()}</strong>
  </div>

  <div class="section">
    <div class="section-title">Executive Summary</div>
    <p>${escapeHTML(report.executive_summary || '')}</p>
  </div>

  <div class="section">
    <div class="section-title">Timeline of Events</div>
    <table>
      <thead><tr><th>Time (UTC)</th><th>Source</th><th>Event</th></tr></thead>
      <tbody>
        ${(report.timeline || []).map(t => `<tr><td>${escapeHTML(t.timestamp)}</td><td>${escapeHTML(t.source)}</td><td>${escapeHTML(t.event)}</td></tr>`).join("")}
      </tbody>
    </table>
  </div>

  <div class="section">
    <div class="section-title">Technical Root Cause Analysis</div>
    <p>${escapeHTML(report.root_cause_technical || '')}</p>
    ${report.stack_trace ? `<pre>${escapeHTML(report.stack_trace)}</pre>` : ''}
  </div>

  <div class="section">
    <div class="section-title">Blast Radius & Multi-Tenant Impact</div>
    <p>${escapeHTML(report.blast_radius || '')}</p>
  </div>

  <div class="section">
    <div class="section-title">Preventative Runbook & Action Items</div>
    ${(report.preventative_runbook || []).map((item) => `<div class="checklist-item">[ ] ${escapeHTML(item)}</div>`).join("")}
  </div>
</body>
</html>`;
  triggerFileDownload(filename, htmlContent, "text/html;charset=utf-8");
}

// -------------------------------------------------------------
// Day-2 Superpower 1: Deployment Revision History & 1-Click Rollback
// -------------------------------------------------------------

let currentRollbackState = { namespace: "", deployment: "", selectedRevision: null };

async function openRollbackModal(namespace, deploymentName) {
  currentRollbackState = { namespace, deployment: deploymentName, selectedRevision: null };
  const modal = $("#admin-rollback-modal");
  modal.classList.add("open");
  $("#rollback-modal-title").textContent = `Rollback: ${deploymentName} (${namespace})`;
  $("#rollback-modal-status").textContent = "";
  $("#rollback-modal-confirm-btn").disabled = true;
  $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state">Fetching revisions…</div>`;
  $("#rollback-diff-pane").innerHTML = `<div class="admin-empty-state">Select a revision on the left to see differences against the active rollout.</div>`;

  try {
    const revs = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/revisions`);
    if (!revs || revs.length === 0) {
      $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state">No ReplicaSet revisions found.</div>`;
      return;
    }

    $("#rollback-revisions-list").innerHTML = `
      <div class="admin-rev-list">
        ${revs.map((r) => `
          <div class="admin-rev-item ${r.is_current ? "active" : ""}" id="rev-item-${r.revision}" onclick="selectRollbackRevision('${escapeHTML(namespace)}', '${escapeHTML(deploymentName)}', ${r.revision})">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px;">
              <strong style="font-size: 13px;">Revision ${r.revision}</strong>
              ${r.is_current ? `<span class="admin-badge admin-badge-ready" style="font-size: 10px;">ACTIVE</span>` : `<span style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(r.age)} ago</span>`}
            </div>
            <div style="font-size: 11px; color: var(--admin-text-muted); word-break: break-all;">
              ${escapeHTML(r.images.join(", "))}
            </div>
          </div>
        `).join("")}
      </div>
    `;

    const previous = revs.find(r => !r.is_current);
    if (previous) {
      selectRollbackRevision(namespace, deploymentName, previous.revision);
    }
  } catch (err) {
    $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to fetch revisions: ${escapeHTML(err.message)}</div>`;
  }
}

async function selectRollbackRevision(namespace, deploymentName, revision) {
  currentRollbackState.selectedRevision = revision;
  $$(".admin-rev-item").forEach(el => el.classList.remove("active"));
  $(`#rev-item-${revision}`)?.classList.add("active");

  const diffPane = $("#rollback-diff-pane");
  diffPane.innerHTML = `<div class="admin-empty-state">Calculating visual diff…</div>`;
  $("#rollback-modal-confirm-btn").disabled = true;

  try {
    const diff = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/revisions/${revision}/diff`);
    
    if (diff.identical) {
      diffPane.innerHTML = `
        <div class="admin-empty-state" style="color: var(--admin-green);">
          <h3>Revision ${revision} is identical to active revision (${diff.current_revision})</h3>
          <p>No changes would be applied by rolling back to this revision.</p>
        </div>
      `;
      $("#rollback-modal-confirm-btn").disabled = true;
      return;
    }

    diffPane.innerHTML = `
      <div style="margin-bottom: 12px; display: flex; justify-content: space-between; align-items: center;">
        <span style="font-size: 12px; font-weight: 600;">Diff: Revision ${revision} (Target) vs Revision ${diff.current_revision} (Active)</span>
        <span class="admin-badge admin-badge-warning">${diff.changes.length} change(s)</span>
      </div>
      <div style="display: flex; flex-direction: column; gap: 8px;">
        ${diff.changes.map(c => `
          <div class="admin-diff-change-row">
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <strong style="color: var(--admin-text);">${escapeHTML(c.field)}</strong>
              <span class="admin-diff-tag ${escapeHTML(c.action)}">${escapeHTML(c.action)}</span>
            </div>
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-top: 4px;">
              <div style="background: #fee2e2; color: #991b1b; padding: 4px 8px; border-radius: 4px; overflow-x: auto;">
                <span style="font-size: 10px; opacity: 0.7; display: block;">Active Spec (Revision ${diff.current_revision}):</span>
                ${escapeHTML(c.old_value)}
              </div>
              <div style="background: #dcfce7; color: #166534; padding: 4px 8px; border-radius: 4px; overflow-x: auto;">
                <span style="font-size: 10px; opacity: 0.7; display: block;">Target Spec (Revision ${revision}):</span>
                ${escapeHTML(c.new_value)}
              </div>
            </div>
          </div>
        `).join("")}
      </div>
    `;

    $("#rollback-modal-confirm-btn").disabled = false;
    $("#rollback-modal-confirm-btn").onclick = () => executeRollback(namespace, deploymentName, revision);
  } catch (err) {
    diffPane.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to compute diff: ${escapeHTML(err.message)}</div>`;
  }
}

async function executeRollback(namespace, deploymentName, targetRevision) {
  const btn = $("#rollback-modal-confirm-btn");
  btn.disabled = true;
  btn.textContent = "Rolling back…";
  $("#rollback-modal-status").textContent = "Triggering rolling deployment rollback…";

  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/rollback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ targetRevision }),
    });

    $("#rollback-modal-status").innerHTML = `<strong style="color: var(--admin-green);">Successfully rolled back to revision ${targetRevision}!</strong>`;
    setTimeout(() => {
      $("#admin-rollback-modal").classList.remove("open");
      btn.textContent = "1-Click Rollback";
      if (state.activeView === "deployments") {
        renderDeployments($("#admin-content-area"));
      } else {
        renderCurrentView();
      }
    }, 1500);
  } catch (err) {
    btn.disabled = false;
    btn.textContent = "1-Click Rollback";
    $("#rollback-modal-status").innerHTML = `<span style="color: var(--admin-red);">Rollback failed: ${escapeHTML(err.message)}</span>`;
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 2: TLS/SSL Certificate Expiration Radar
// -------------------------------------------------------------

async function renderCertificates(container) {
  const report = await request("/api/v1/admin/security/certificates");
  const filtered = filterRows(report.certificates || [], ["secret_name", "namespace", "common_name", "issuer", "status"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>TLS/SSL Certificate Expiration Radar</h1>
        <span class="admin-view-count">${report.total_certificates}</span>
      </div>
    </div>

    <!-- Metrics Cards -->
    <div class="admin-kpi-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Valid Certificates</div>
        <div class="admin-kpi-val" style="color: var(--admin-green);">${report.valid_count}</div>
        <div class="admin-kpi-sub">> 30 days remaining</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Expiring Soon</div>
        <div class="admin-kpi-val" style="color: var(--admin-amber);">${report.expiring_soon_count}</div>
        <div class="admin-kpi-sub">Within 30 days</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Critical Urgency</div>
        <div class="admin-kpi-val" style="color: var(--admin-red);">${report.critical_count}</div>
        <div class="admin-kpi-sub">Within 7 days</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Expired Certificates</div>
        <div class="admin-kpi-val" style="color: #64748b;">${report.expired_count}</div>
        <div class="admin-kpi-sub">Immediate outage risk</div>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Secret / Cert</th>
            <th>Namespace</th>
            <th>Common Name (CN)</th>
            <th>Subject Alternative Names (SANs)</th>
            <th>Issuer</th>
            <th>Expires On</th>
            <th>Days Remaining</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No TLS secret certificates found in cluster.</td></tr>` : ""}
          ${filtered.map(c => {
            let statusBadge = `<span class="admin-badge admin-badge-ready">Valid</span>`;
            if (c.status === "expiring_soon") statusBadge = `<span class="admin-badge admin-badge-warning">Expiring Soon</span>`;
            if (c.status === "critical") statusBadge = `<span class="admin-badge admin-badge-danger">Critical (&lt; 7d)</span>`;
            if (c.status === "expired") statusBadge = `<span class="admin-badge" style="background:#fee2e2;color:#991b1b;border:1px solid #f87171;">Expired</span>`;

            return `
              <tr>
                <td class="admin-table-name">${escapeHTML(c.secret_name)}</td>
                <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
                <td><strong>${escapeHTML(c.common_name)}</strong></td>
                <td style="font-size: 11px; max-width: 250px; overflow: hidden; text-overflow: ellipsis;">
                  ${escapeHTML((c.dns_names || []).join(", ") || "-")}
                </td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(c.issuer)} ${c.is_self_signed ? `<span class="admin-badge" style="font-size: 9px;">Self-Signed</span>` : ""}</td>
                <td style="font-size: 11px; font-family: var(--admin-mono);">${escapeHTML(c.not_after)}</td>
                <td>
                  <strong style="color: ${c.days_remaining <= 7 ? "var(--admin-red)" : c.days_remaining <= 30 ? "var(--admin-amber)" : "var(--admin-green)"}">
                    ${c.days_remaining <= 0 ? "EXPIRED" : `${c.days_remaining} days`}
                  </strong>
                </td>
                <td>${statusBadge}</td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// Day-2 Superpower 3: Node Drain & Maintenance Safety Simulator
// -------------------------------------------------------------

async function openNodeDrainModal(nodeName) {
  const modal = $("#admin-drain-modal");
  modal.classList.add("open");
  $("#drain-modal-title").textContent = `Drain Simulation: ${nodeName}`;
  $("#drain-modal-status").textContent = "";
  $("#drain-modal-body").innerHTML = `<div class="admin-empty-state">Running live capacity simulation for ${escapeHTML(nodeName)}…</div>`;

  try {
    const sim = await request(`/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/drain-simulation`);

    const isSafe = sim.status === "safe";
    const statusColor = isSafe ? "var(--admin-green)" : sim.status === "warning" ? "var(--admin-amber)" : "var(--admin-red)";
    $("#drain-modal-badge").textContent = sim.status.toUpperCase();
    $("#drain-modal-badge").style.borderColor = statusColor;
    $("#drain-modal-badge").style.color = statusColor;

    const cordonBtn = $("#drain-modal-cordon-btn");
    if (sim.unschedulable) {
      cordonBtn.textContent = "Uncordon Node";
      cordonBtn.className = "admin-btn admin-btn-primary";
      cordonBtn.onclick = () => toggleCordonNode(nodeName, false);
    } else {
      cordonBtn.textContent = "Cordon Node (Mark Unschedulable)";
      cordonBtn.className = "admin-btn admin-btn-danger";
      cordonBtn.onclick = () => toggleCordonNode(nodeName, true);
    }

    $("#drain-modal-body").innerHTML = `
      <div style="background: #f8fafc; border: 1px solid var(--admin-border); border-radius: 8px; padding: 16px; margin-bottom: 16px;">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
          <div>
            <div style="font-size: 11px; text-transform: uppercase; font-weight: 700; color: var(--admin-text-muted);">Cluster Headroom Absorption</div>
            <strong style="font-size: 15px; color: ${sim.cluster_headroom.can_absorb_workloads ? "var(--admin-green)" : "var(--admin-red)"};">
              ${sim.cluster_headroom.can_absorb_workloads ? "Surviving Nodes Can Absorb Workloads" : "Insufficient Headroom: Pods Will Pending"}
            </strong>
          </div>
          <span class="admin-badge ${sim.unschedulable ? "admin-badge-warning" : "admin-badge-ready"}">
            ${sim.unschedulable ? "Currently Cordoned" : "Active & Schedulable"}
          </span>
        </div>

        <div style="display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; font-size: 12px;">
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Surviving Ready Nodes:</span>
            <strong>${sim.cluster_headroom.available_nodes}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Available Headroom (CPU/Mem):</span>
            <strong>${sim.cluster_headroom.headroom_cpu} / ${sim.cluster_headroom.headroom_memory}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Required Eviction (CPU/Mem):</span>
            <strong style="color: var(--admin-accent);">${sim.cluster_headroom.required_evict_cpu} / ${sim.cluster_headroom.required_evict_memory}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Evictable Pods:</span>
            <strong>${sim.total_evictable_pods} pods (${sim.singleton_pod_count} singleton)</strong>
          </div>
        </div>
      </div>

      ${sim.safety_recommendations && sim.safety_recommendations.length > 0 ? `
        <div style="background: #fff7ed; border: 1px solid #fed7aa; border-radius: 8px; padding: 12px 16px; margin-bottom: 16px;">
          <strong style="font-size: 12px; color: #9a3412;">Safety Recommendations:</strong>
          <ul style="margin: 6px 0 0; padding-left: 18px; font-size: 12px; color: #7c2d12;">
            ${sim.safety_recommendations.map(r => `<li>${escapeHTML(r)}</li>`).join("")}
          </ul>
        </div>
      ` : ""}

      <div style="font-size: 12px; font-weight: 700; margin-bottom: 8px;">Pods to be Evicted (${sim.eviction_candidates.length})</div>
      <div style="max-height: 240px; overflow-y: auto; border: 1px solid var(--admin-border); border-radius: 6px;">
        <table class="admin-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th>Pod Name</th>
              <th>Namespace</th>
              <th>Workload</th>
              <th>Singleton Warning</th>
              <th>CPU Req</th>
              <th>Mem Req</th>
            </tr>
          </thead>
          <tbody>
            ${sim.eviction_candidates.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No non-DaemonSet pods scheduled on this node.</td></tr>` : ""}
            ${sim.eviction_candidates.map(p => `
              <tr>
                <td class="admin-table-name">${escapeHTML(p.name)}</td>
                <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
                <td>${escapeHTML(p.workload_kind)}/${escapeHTML(p.workload_name)}</td>
                <td>
                  ${p.is_singleton ? `<span class="admin-badge admin-badge-danger">Singleton (Downtime Risk)</span>` : `<span class="admin-badge admin-badge-ready">Replicated</span>`}
                </td>
                <td style="font-family: var(--admin-mono);">${escapeHTML(p.cpu_request)}</td>
                <td style="font-family: var(--admin-mono);">${escapeHTML(p.memory_request)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    $("#drain-modal-body").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Simulation failed: ${escapeHTML(err.message)}</div>`;
  }
}

async function toggleCordonNode(nodeName, unschedulable) {
  const btn = $("#drain-modal-cordon-btn");
  btn.disabled = true;
  const endpoint = unschedulable ? `/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/cordon` : `/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/uncordon`;

  try {
    await request(endpoint, { method: "POST" });
    $("#drain-modal-status").innerHTML = `<strong style="color: var(--admin-green);">Node ${escapeHTML(nodeName)} ${unschedulable ? "cordoned" : "uncordoned"} successfully!</strong>`;
    setTimeout(() => {
      openNodeDrainModal(nodeName);
      if (state.activeView === "nodes") renderNodes($("#admin-content-area"));
    }, 1000);
  } catch (err) {
    btn.disabled = false;
    $("#drain-modal-status").innerHTML = `<span style="color: var(--admin-red);">Action failed: ${escapeHTML(err.message)}</span>`;
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 4: Incident Webhook Alert Dispatcher
// -------------------------------------------------------------

async function renderWebhooks(container) {
  const webhooks = await request("/api/v1/admin/webhooks");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Incident Webhook Alerts</h1>
        <span class="admin-view-count">${webhooks.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="openWebhookModal()" type="button">
          + Register Webhook Target
        </button>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Destination Name</th>
            <th>Provider Format</th>
            <th>Webhook Endpoint</th>
            <th>Trigger Events</th>
            <th>Created</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${webhooks.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No webhooks registered. Register a Slack, Discord, or generic destination to dispatch real-time outage alerts.</td></tr>` : ""}
          ${webhooks.map(w => {
            let providerBadge = `<span class="admin-badge admin-badge-info">${escapeHTML(w.provider.toUpperCase())}</span>`;
            if (w.provider === "slack") providerBadge = `<span class="admin-badge" style="background:#e0e7ff;color:#3730a3;border:1px solid #c7d2fe;">SLACK</span>`;
            if (w.provider === "discord") providerBadge = `<span class="admin-badge" style="background:#ede9fe;color:#5b21b6;border:1px solid #ddd6fe;">DISCORD</span>`;

            return `
              <tr>
                <td class="admin-table-name"><strong>${escapeHTML(w.name)}</strong></td>
                <td>${providerBadge}</td>
                <td class="admin-table-ns" style="max-width: 250px; overflow: hidden; text-overflow: ellipsis;" title="${escapeHTML(w.url)}">
                  ${escapeHTML(w.url)}
                </td>
                <td style="font-size: 11px;">${escapeHTML(w.events)}</td>
                <td class="admin-table-age">${escapeHTML(w.created_at)}</td>
                <td>
                  <div class="admin-row-actions" style="justify-content: flex-end;">
                    <button class="admin-action-btn" onclick="testWebhook(${w.id})" title="Dispatch Test Alert">Test Alert</button>
                    <button class="admin-action-btn btn-delete" onclick="deleteWebhook(${w.id})" title="Delete Webhook">Delete</button>
                  </div>
                </td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

function openWebhookModal() {
  $("#webhook-name-input").value = "";
  $("#webhook-url-input").value = "";
  $("#webhook-provider-select").value = "slack";
  $("#webhook-events-input").value = "critical, pod_crash, node_pressure, cert_expiring";
  $("#admin-webhook-modal").classList.add("open");
}

async function handleCreateWebhook(e) {
  e.preventDefault();
  const name = $("#webhook-name-input").value.trim();
  const url = $("#webhook-url-input").value.trim();
  const provider = $("#webhook-provider-select").value;
  const events = $("#webhook-events-input").value.trim();

  try {
    await request("/api/v1/admin/webhooks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, provider, events }),
    });
    $("#admin-webhook-modal").classList.remove("open");
    renderWebhooks($("#admin-content-area"));
  } catch (err) {
    alert("Could not register webhook: " + err.message);
  }
}

async function deleteWebhook(id) {
  if (!confirm("Are you sure you want to delete this incident webhook target?")) return;
  try {
    await request(`/api/v1/admin/webhooks/${id}`, { method: "DELETE" });
    renderWebhooks($("#admin-content-area"));
  } catch (err) {
    alert("Delete failed: " + err.message);
  }
}

async function testWebhook(id) {
  try {
    const res = await request(`/api/v1/admin/webhooks/${id}/test`, { method: "POST" });
    if (res.success) {
      alert(`Test alert delivered successfully! (HTTP ${res.status_code})`);
    } else {
      alert(`Test alert failed: ${res.message}`);
    }
  } catch (err) {
    alert("Test dispatch failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 5: Broken Reference & Dangling Config Scanner
// -------------------------------------------------------------

async function renderDanglingConfigs(container) {
  const report = await request("/api/v1/admin/security/dangling-configs");
  const filtered = filterRows(report.issues || [], ["workload_name", "namespace", "missing_name", "missing_kind"]);

  const badge = $("#badge-dangling");
  if (badge) {
    badge.textContent = report.broken_reference_count;
    badge.hidden = report.broken_reference_count === 0;
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Broken Reference & Dangling Config Pre-Flight Scanner</h1>
        <span class="admin-view-count">${report.broken_reference_count}</span>
      </div>
    </div>

    <!-- Overview Hero -->
    <div style="background: ${report.broken_reference_count === 0 ? "#f0fdf4" : "#fef2f2"}; border: 1px solid ${report.broken_reference_count === 0 ? "#bbf7d0" : "#fecaca"}; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; align-items: center;">
        <div>
          <h3 style="margin: 0 0 4px; font-size: 15px; color: ${report.broken_reference_count === 0 ? "#166534" : "#991b1b"};">
            ${report.broken_reference_count === 0 ? "Zero Dangling Configuration References Detected" : `${report.broken_reference_count} Broken Reference(s) Causing CreateContainerConfigError`}
          </h3>
          <p style="margin: 0; font-size: 13px; color: ${report.broken_reference_count === 0 ? "#15803d" : "#b91c1c"};">
            ${report.broken_reference_count === 0 ? `Scanned ${report.total_scanned_workloads} workloads across all namespaces. All referenced ConfigMaps, Secrets, keys, and volumes exist.` : `Found missing objects or keys referenced in env/envFrom/volumes. Workloads will fail on restart.`}
          </p>
        </div>
        <span class="admin-badge ${report.broken_reference_count === 0 ? "admin-badge-ready" : "admin-badge-danger"}">
          ${report.total_scanned_workloads} Workloads Scanned
        </span>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Workload</th>
            <th>Namespace</th>
            <th>Container</th>
            <th>Reference Type</th>
            <th>Missing Resource</th>
            <th>Missing Key</th>
            <th>Remediation Runbook</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No dangling or missing configuration references detected! All workloads are clean.</td></tr>` : ""}
          ${filtered.map(issue => `
            <tr>
              <td class="admin-table-name">
                <span class="admin-kind-badge">${escapeHTML(issue.workload_kind)}</span>
                <strong>${escapeHTML(issue.workload_name)}</strong>
              </td>
              <td class="admin-table-ns">${escapeHTML(issue.namespace)}</td>
              <td><code>${escapeHTML(issue.container)}</code></td>
              <td><span class="admin-badge" style="font-size: 10px;">${escapeHTML(issue.ref_type)}</span></td>
              <td>
                <span class="admin-badge admin-badge-danger">${escapeHTML(issue.missing_kind)}</span>
                <strong style="color: var(--admin-red);">${escapeHTML(issue.missing_name)}</strong>
              </td>
              <td style="font-family: var(--admin-mono);">${issue.missing_key ? escapeHTML(issue.missing_key) : `<span style="color: var(--admin-text-muted);">(entire object missing)</span>`}</td>
              <td style="font-size: 11px; color: var(--admin-text-muted); max-width: 320px;">${escapeHTML(issue.remediation)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// Next-Gen Capability 1: In-Browser Ephemeral Debug Containers
// -------------------------------------------------------------

function openEphemeralDebugModal(namespace, podName) {
  const modal = $("#admin-debug-modal");
  if (!modal) return;
  $("#debug-target-pod").value = `${namespace}/${podName}`;
  $("#debug-target-container").value = "";
  $("#debug-image-select").value = "nicolaka/netshoot";
  $("#debug-custom-image-wrap").hidden = true;
  $("#debug-custom-image-input").value = "";
  modal.dataset.namespace = namespace;
  modal.dataset.pod = podName;
  modal.classList.add("open");
}

function closeEphemeralDebugModal() {
  $("#admin-debug-modal")?.classList.remove("open");
}

async function handleLaunchDebugContainer(e) {
  e.preventDefault();
  const modal = $("#admin-debug-modal");
  const ns = modal.dataset.namespace;
  const pod = modal.dataset.pod;
  const selectVal = $("#debug-image-select").value;
  let image = selectVal;
  if (selectVal === "custom") {
    image = $("#debug-custom-image-input").value.trim();
    if (!image) {
      alert("Please provide a valid custom image URI.");
      return;
    }
  }
  const targetContainer = $("#debug-target-container").value.trim();
  const launchBtn = $("#debug-modal-launch-btn");
  launchBtn.disabled = true;
  launchBtn.textContent = "Injecting Debug Container…";

  try {
    const resp = await request(`/api/v1/admin/pods/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}/debug`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        image: image,
        target_container: targetContainer,
      }),
    });
    closeEphemeralDebugModal();
    // Directly attach interactive terminal to the newly injected container
    openPodTerminal(ns, resp.pod_name, resp.container_name);
  } catch (err) {
    alert("Debug container injection failed: " + err.message);
  } finally {
    launchBtn.disabled = false;
    launchBtn.textContent = "Launch Debug Shell";
  }
}

// -------------------------------------------------------------
// Next-Gen Capability 2: Live Event Storm & Velocity Radar
// -------------------------------------------------------------

async function renderEventStorm(container) {
  let storm;
  try {
    storm = await request("/api/v1/admin/events/storm");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Event Storm Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to monitor cluster event storms and velocity anomalies.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const clusters = storm.clusters || [];
  let statusBadge = `<span class="admin-storm-badge normal">NORMAL VELOCITY</span>`;
  if (storm.status === "critical") statusBadge = `<span class="admin-storm-badge critical">CRITICAL STORM</span>`;
  else if (storm.status === "warning") statusBadge = `<span class="admin-storm-badge warning">ELEVATED NOISE</span>`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Live Event Storm & Anomaly Radar</h1>
        <span class="admin-view-count">${clusters.length} Clusters</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderEventStorm($('#admin-content-area'))">Refresh Radar</button>
      </div>
    </div>

    <!-- Storm Stat Cards -->
    <div class="admin-storm-grid">
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Status</div>
        <div style="margin: 8px 0;">${statusBadge}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Anomaly Score: <strong>${storm.anomaly_score}/100</strong></div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Cluster Velocity</div>
        <div style="font-size: 26px; font-weight: 800; color: ${storm.velocity_per_minute > 20 ? "var(--admin-red)" : "var(--admin-accent)"}; margin: 4px 0;">
          ${storm.velocity_per_minute.toFixed(1)} <span style="font-size: 13px; font-weight: 500; color: var(--admin-text-muted);">events / min</span>
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Total Ingestion Window: <strong>${storm.total_events} events</strong></div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Event Suppression</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">
          ${clusters.length > 0 ? ((1 - clusters.length / Math.max(storm.total_events, 1)) * 100).toFixed(0) : 100}%
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Noise reduction ratio via root-clustering</div>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Anomaly Score</th>
            <th>Reason & Kind</th>
            <th>Namespace</th>
            <th>Count</th>
            <th>Velocity</th>
            <th>Sample Targets</th>
            <th>Latest Event Signature</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody>
          ${clusters.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No repetitive event storms or warning clusters detected. Cluster event baseline is calm.</td></tr>` : ""}
          ${clusters.map((c) => {
            let scoreClass = "admin-badge-ready";
            if (c.anomaly_score >= 70) scoreClass = "admin-badge-danger";
            else if (c.anomaly_score >= 35) scoreClass = "admin-badge-warning";

            return `
              <tr>
                <td><span class="admin-badge ${scoreClass}">${c.anomaly_score} / 100</span></td>
                <td>
                  <span class="admin-kind-badge">${escapeHTML(c.kind)}</span>
                  <strong style="color: var(--admin-red); font-size: 12px;">${escapeHTML(c.reason)}</strong>
                </td>
                <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
                <td style="font-weight: 700;">${c.count}</td>
                <td>
                  <div class="admin-velocity-meter">
                    <span>${c.velocity.toFixed(1)}/m</span>
                    <div class="admin-velocity-bar-wrap">
                      <div class="admin-velocity-bar-fill" style="width: ${Math.min(100, c.velocity * 5)}%;"></div>
                    </div>
                  </div>
                </td>
                <td>
                  <div style="display: flex; gap: 4px; flex-wrap: wrap; max-width: 220px;">
                    ${(c.sample_objects || []).slice(0, 3).map((obj) => `<span class="admin-badge" style="font-size: 10px;">${escapeHTML(obj)}</span>`).join("")}
                  </div>
                </td>
                <td style="font-size: 11px; color: var(--admin-text-muted); max-width: 340px; line-height: 1.4;">
                  ${escapeHTML(c.message)}
                </td>
                <td style="text-align: right;">
                  <a class="admin-btn admin-btn-sm" href="#war-room?ns=${encodeURIComponent(c.namespace)}">War Room</a>
                </td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// Next-Gen Capability 3: Autoscaling & HPA Flapping Detector
// -------------------------------------------------------------

async function renderAutoscaling(container) {
  let res;
  try {
    res = await request("/api/v1/admin/autoscaling");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Autoscaling & HPA</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to inspect HorizontalPodAutoscalers and scaling limits.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const hpas = res.hpas || [];

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Autoscaling & HPA Flapping Detector</h1>
        <span class="admin-view-count">${res.total_hpas} HPAs</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderAutoscaling($('#admin-content-area'))">Refresh</button>
      </div>
    </div>

    <!-- HPA KPI Cards -->
    <div class="admin-storm-grid">
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Managed Autoscalers</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">${res.total_hpas}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Configured HorizontalPodAutoscalers</div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">At Scaling Ceiling</div>
        <div style="font-size: 26px; font-weight: 800; color: ${res.at_capacity_count > 0 ? "var(--admin-red)" : "var(--admin-green)"}; margin: 4px 0;">
          ${res.at_capacity_count}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Replicas capped at maximum ceiling limit</div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Flapping HPAs</div>
        <div style="font-size: 26px; font-weight: 800; color: ${res.flapping_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"}; margin: 4px 0;">
          ${res.flapping_count}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Scaling limited or oscillation detected</div>
      </div>
    </div>

    <!-- HPA Cards Grid -->
    ${hpas.length === 0 ? `
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 40px 16px;">
          <h3>No HorizontalPodAutoscalers Found</h3>
          <p style="color: var(--admin-text-muted); max-width: 420px; margin: 8px auto 0;">
            No HPA resources are currently defined in this cluster. Add an HPA to enable automatic horizontal workload scaling.
          </p>
        </div>
      </div>
    ` : `
      <div class="admin-hpa-grid">
        ${hpas.map((h) => {
          let badge = `<span class="admin-badge admin-badge-ready">HEALTHY</span>`;
          let headroomClass = "";
          if (h.status === "at_capacity") {
            badge = `<span class="admin-badge admin-badge-danger">CEILING REACHED</span>`;
            headroomClass = "danger";
          } else if (h.status === "flapping") {
            badge = `<span class="admin-badge admin-badge-warning">FLAPPING / LIMITED</span>`;
            headroomClass = "warning";
          }

          return `
            <div class="admin-hpa-card">
              <div class="admin-hpa-header">
                <div>
                  <div style="font-weight: 700; font-size: 14px; margin-bottom: 2px;">${escapeHTML(h.name)}</div>
                  <div class="admin-table-ns">${escapeHTML(h.namespace)} &bull; ${escapeHTML(h.target_kind)}/${escapeHTML(h.target_name)}</div>
                </div>
                ${badge}
              </div>

              <div>
                <div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px;">
                  <span>Replicas: <strong>${h.current_replicas}</strong> (Desired: ${h.desired_replicas})</span>
                  <span style="color: var(--admin-text-muted);">Range: [${h.min_replicas} - ${h.max_replicas}]</span>
                </div>
                <div class="admin-headroom-bar">
                  <div class="admin-headroom-fill ${headroomClass}" style="width: ${Math.max(4, h.headroom_pct)}%;"></div>
                </div>
                <div style="display: flex; justify-content: space-between; font-size: 11px; color: var(--admin-text-muted);">
                  <span>Headroom: ${h.headroom_pct.toFixed(0)}%</span>
                  <span>${h.current_replicas >= h.max_replicas ? "Maxed Out" : `${h.max_replicas - h.current_replicas} Pods Available`}</span>
                </div>
              </div>

              <div>
                <div style="font-size: 11px; font-weight: 600; text-transform: uppercase; color: var(--admin-text-muted); margin-bottom: 4px;">Active Metrics</div>
                <span class="admin-hpa-metric-pill">${escapeHTML(h.metrics_summary || "cpu: target pending")}</span>
              </div>

              ${h.reason ? `
                <div style="background: var(--admin-bg-secondary); padding: 8px 10px; border-radius: 4px; font-size: 11px; color: ${h.status === "at_capacity" ? "var(--admin-red)" : "var(--admin-amber)"}; line-height: 1.4;">
                  ${escapeHTML(h.reason)}
                </div>
              ` : ""}

              <div style="margin-top: auto; display: flex; justify-content: flex-end; gap: 8px;">
                <button class="admin-btn admin-btn-sm" onclick="openScaleModal('${escapeHTML(h.namespace)}', '${escapeHTML(h.target_name)}', ${h.current_replicas})">Adjust Scale</button>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    `}
  `;
}

// -------------------------------------------------------------
// Next-Gen Capability 4: GitOps Drift & Out-of-Band Mutation Radar
// -------------------------------------------------------------

async function renderGitOpsDrift(container) {
  let report;
  try {
    report = await request("/api/v1/admin/gitops/drift");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>GitOps Drift Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to detect out-of-band mutations and GitOps drift.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const drifts = (report.drifts || []).filter((d) => d.has_drift);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>GitOps Drift & Out-of-Band Mutation Radar</h1>
        <span class="admin-view-count">${report.drift_count} Drifted</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderGitOpsDrift($('#admin-content-area'))">Scan Drift</button>
      </div>
    </div>

    <!-- Drift Banner -->
    <div style="background: ${report.drift_count === 0 ? "var(--admin-green-bg)" : "var(--admin-red-bg)"}; border: 1px solid ${report.drift_count === 0 ? "var(--admin-green-border)" : "var(--admin-red-border)"}; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; align-items: center;">
        <div>
          <h3 style="margin: 0 0 4px; font-size: 15px; color: ${report.drift_count === 0 ? "var(--admin-green)" : "var(--admin-red)"};">
            ${report.drift_count === 0 ? "All Workloads In Sync with Declared GitOps State" : `${report.drift_count} Out-of-Band Mutation(s) Detected`}
          </h3>
          <p style="margin: 0; font-size: 13px; color: ${report.drift_count === 0 ? "var(--admin-green)" : "var(--admin-red)"};">
            ${report.drift_count === 0 ? `Scanned ${report.total_scanned} managed workloads against 'kubectl.kubernetes.io/last-applied-configuration'. Zero configuration drift.` : `Live cluster state differs from the declarative GitOps specification. Use 1-Click Reconcile to restore intended state.`}
          </p>
        </div>
        <span class="admin-badge ${report.drift_count === 0 ? "admin-badge-ready" : "admin-badge-danger"}">
          ${report.total_scanned} Workloads Scanned
        </span>
      </div>
    </div>

    ${drifts.length === 0 ? `
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 48px 16px;">
          <h3>Cluster Declarative State Intact</h3>
          <p style="color: var(--admin-text-muted); max-width: 440px; margin: 8px auto 0;">
            No out-of-band hotfixes, ad-hoc image mutations, or uncommitted replica modifications detected.
          </p>
        </div>
      </div>
    ` : `
      <div>
        ${drifts.map((d) => `
          <div class="admin-drift-card">
            <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 10px;">
              <div>
                <span class="admin-kind-badge">${escapeHTML(d.kind)}</span>
                <strong style="font-size: 15px;">${escapeHTML(d.name)}</strong>
                <span class="admin-table-ns" style="margin-left: 8px;">ns: ${escapeHTML(d.namespace)}</span>
              </div>
              <div style="display: flex; gap: 8px; align-items: center;">
                <span class="admin-badge admin-badge-warning" style="text-transform: uppercase;">${escapeHTML(d.drift_type || "mutation")}</span>
                <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="reconcileGitOps('${escapeHTML(d.kind)}', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">1-Click Reconcile</button>
              </div>
            </div>

            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 10px;">
              <div style="background: var(--admin-bg-secondary); padding: 10px; border-radius: 6px; font-size: 12px;">
                <div style="font-weight: 700; color: var(--admin-text-muted); margin-bottom: 4px;">DECLARED (GitOps)</div>
                <code>${escapeHTML(d.declared_summary)}</code>
              </div>
              <div style="background: var(--admin-bg-secondary); padding: 10px; border-radius: 6px; font-size: 12px;">
                <div style="font-weight: 700; color: var(--admin-red); margin-bottom: 4px;">LIVE MUTATION</div>
                <code>${escapeHTML(d.live_summary)}</code>
              </div>
            </div>

            <div class="admin-diff-box">
              ${(d.diff || "").split("\n").map((line) => {
                let cls = "";
                if (line.startsWith("-")) cls = "del";
                else if (line.startsWith("+")) cls = "add";
                return `<div class="admin-diff-line ${cls}">${escapeHTML(line)}</div>`;
              }).join("")}
            </div>
          </div>
        `).join("")}
      </div>
    `}
  `;
}

async function reconcileGitOps(kind, namespace, name) {
  if (!confirm(`Are you sure you want to reconcile ${kind}/${name} in ${namespace} back to its declared GitOps configuration? This will undo any live out-of-band mutations.`)) {
    return;
  }
  try {
    const res = await request("/api/v1/admin/gitops/reconcile", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ kind, namespace, name }),
    });
    alert(res.message || "Reconciliation complete.");
    renderGitOpsDrift($("#admin-content-area"));
  } catch (err) {
    alert("Reconciliation failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Next-Gen Capability 5: Cluster Disaster Recovery & Backup Radar
// -------------------------------------------------------------

async function renderDisasterRecoveryRadar(container) {
  let dr;
  try {
    dr = await request("/api/v1/admin/dr/radar");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Disaster Recovery Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to evaluate disaster recovery coverage and PVC backup schedules.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const schedules = dr.schedules || [];
  const pvcs = dr.pvc_protection || [];

  let scoreColorClass = "good";
  if (dr.dr_score < 50) scoreColorClass = "poor";
  else if (dr.dr_score < 75) scoreColorClass = "warning";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Disaster Recovery & Backup Radar</h1>
        <span class="admin-view-count">Score: ${dr.dr_score}/100</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderDisasterRecoveryRadar($('#admin-content-area'))">Refresh Coverage</button>
      </div>
    </div>

    <!-- DR KPI Row -->
    <div class="admin-dr-stats">
      <div class="admin-storm-card" style="display: flex; align-items: center; gap: 16px;">
        <div class="admin-dr-score-circle ${scoreColorClass}">
          ${dr.dr_score}
          <span style="font-size: 10px; font-weight: 600;">/ 100</span>
        </div>
        <div>
          <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">DR Compliance</div>
          <div style="font-size: 15px; font-weight: 700; text-transform: capitalize; margin: 2px 0;">
            ${dr.compliance_status} Coverage
          </div>
          <div style="font-size: 11px; color: var(--admin-text-muted);">Backup provider: <strong>${escapeHTML(dr.backup_tool || "none")}</strong></div>
        </div>
      </div>

      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Persistent Storage Protection</div>
        <div style="font-size: 26px; font-weight: 800; color: ${dr.unbacked_pvcs > 0 ? "var(--admin-red)" : "var(--admin-green)"}; margin: 4px 0;">
          ${dr.protected_pvcs} / ${dr.total_pvcs}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">${dr.unbacked_pvcs} unbacked PersistentVolumeClaim(s)</div>
      </div>

      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Backup Schedules</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">${schedules.length}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Automated snapshot / backup cron jobs</div>
      </div>
    </div>

    <!-- Schedules Table -->
    <h2 style="font-size: 14px; margin: 18px 0 8px;">Active Backup Schedules</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Schedule Name</th>
            <th>Cron Expression</th>
            <th>Last Backup</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          ${schedules.length === 0 ? `<tr><td colspan="4" class="admin-empty-state">No Velero or CSI snapshot schedules detected. Storage volumes lack automated backup policies.</td></tr>` : ""}
          ${schedules.map((s) => `
            <tr>
              <td><strong>${escapeHTML(s.name)}</strong></td>
              <td><code>${escapeHTML(s.schedule)}</code></td>
              <td>${escapeHTML(s.last_backup || "Never")}</td>
              <td><span class="admin-badge admin-badge-ready">${escapeHTML(s.status || "Active")}</span></td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <!-- PVC Protection Table -->
    <h2 style="font-size: 14px; margin: 18px 0 8px;">Persistent Volume Protection & RPO Radar</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>PVC Name</th>
            <th>Namespace</th>
            <th>Capacity</th>
            <th>StorageClass</th>
            <th>Protection Status</th>
            <th>Schedule</th>
            <th>Target RPO</th>
          </tr>
        </thead>
        <tbody>
          ${pvcs.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No PersistentVolumeClaims detected in cluster.</td></tr>` : ""}
          ${pvcs.map((p) => `
            <tr>
              <td><strong>${escapeHTML(p.name)}</strong></td>
              <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
              <td>${escapeHTML(p.size)}</td>
              <td class="admin-table-ns">${escapeHTML(p.storage_class || "standard")}</td>
              <td>
                <span class="admin-badge ${p.is_protected ? "admin-badge-ready" : "admin-badge-danger"}">
                  ${p.is_protected ? "PROTECTED" : "UNBACKED"}
                </span>
              </td>
              <td>${p.backup_schedule ? `<code>${escapeHTML(p.backup_schedule)}</code>` : `<span style="color: var(--admin-text-muted); font-size: 11px;">None</span>`}</td>
              <td style="font-weight: 600;">${p.rpo_hours > 0 ? `&le; ${p.rpo_hours}h` : `<span style="color: var(--admin-red);">N/A (At Risk)</span>`}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// =============================================================
// Enterprise Suite: Core Capabilities for Crelan Platform
// =============================================================

// Helper: Lightweight YAML Formatter for objects
function objectToYAML(obj, indent = 0) {
  if (obj === null || obj === undefined) return "null\n";
  if (typeof obj === "string") {
    if (obj.includes("\n")) {
      return "|\n" + obj.split("\n").map(line => "  ".repeat(indent + 1) + line).join("\n") + "\n";
    }
    return (obj.includes(":") || obj.includes("#") || obj.startsWith("-")) ? JSON.stringify(obj) + "\n" : obj + "\n";
  }
  if (typeof obj === "number" || typeof obj === "boolean") return String(obj) + "\n";
  const pad = "  ".repeat(indent);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return "[]\n";
    return obj.map(item => {
      if (typeof item === "object" && item !== null) {
        const sub = objectToYAML(item, indent + 1);
        return `${pad}- ${sub.trimStart()}`;
      }
      return `${pad}- ${objectToYAML(item, 0)}`;
    }).join("");
  }
  const keys = Object.keys(obj);
  if (keys.length === 0) return "{}\n";
  return keys.map(k => {
    const val = obj[k];
    if (typeof val === "object" && val !== null && !Array.isArray(val) && Object.keys(val).length > 0) {
      return `${pad}${k}:\n${objectToYAML(val, indent + 1)}`;
    } else if (Array.isArray(val)) {
      if (val.length === 0) return `${pad}${k}: []\n`;
      return `${pad}${k}:\n${objectToYAML(val, indent + 1)}`;
    }
    return `${pad}${k}: ${objectToYAML(val, 0)}`;
  }).join("");
}

// -------------------------------------------------------------
// Container Image Vulnerability Scanner (CVE Radar)
// -------------------------------------------------------------
let currentCVEImages = [];

async function renderImageVulnerabilities(container) {
  container.innerHTML = `<div class="admin-empty-state">Scanning active cluster workloads for vulnerabilities…</div>`;
  const data = await request("/api/v1/admin/security/image-vulnerabilities");
  currentCVEImages = data.images || [];
  const summary = data.summary || {
    total_images: 0,
    scanned_images: 0,
    critical_count: 0,
    high_count: 0,
    medium_count: 0,
    low_count: 0,
    clean_images: 0,
  };

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Container Image Vulnerability Scanner (CVE Radar)</h1>
        <p class="admin-view-subtitle">Real-time vulnerability scanner across all pod images with severity scoring, package impact, and remediation guidance.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('cve')">⌘K Quick Scan</button>
        <button class="admin-btn admin-btn-primary" onclick="renderImageVulnerabilities($('#admin-content-area'))">Re-scan Cluster Images</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Images Evaluated</span><span>Workloads</span></div>
        <div class="admin-stat-value">${summary.scanned_images} / ${summary.total_images}</div>
        <div class="admin-stat-subtext">${summary.clean_images} clean without known vulnerabilities</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Critical CVEs</span><span class="cve-badge-critical">CVSS 9.0+</span></div>
        <div class="admin-stat-value" style="color: ${summary.critical_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${summary.critical_count}</div>
        <div class="admin-stat-subtext">Immediate remote code execution / root exploits</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>High Severity CVEs</span><span class="cve-badge-high">CVSS 7.0-8.9</span></div>
        <div class="admin-stat-value" style="color: ${summary.high_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${summary.high_count}</div>
        <div class="admin-stat-subtext">Privilege escalation &amp; authentication bypass</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Medium &amp; Low CVEs</span><span class="cve-badge-medium">CVSS &lt; 7.0</span></div>
        <div class="admin-stat-value">${summary.medium_count + summary.low_count}</div>
        <div class="admin-stat-subtext">${summary.medium_count} medium, ${summary.low_count} low severity</div>
      </div>
    </div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="cve-search-input" class="admin-input" placeholder="Filter by image name or workload..." style="width: 320px;" oninput="filterCVETable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="cve-table-count">${currentCVEImages.length} images evaluated</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Container Image</th>
            <th>Workload Context</th>
            <th>Vulnerabilities Breakdown</th>
            <th>Peak CVSS</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody id="cve-table-body">
          ${renderCVETableRows(currentCVEImages)}
        </tbody>
      </table>
    </div>
  `;
}

function filterCVETable() {
  const query = ($("#cve-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentCVEImages.filter(img => {
    if (img.image.toLowerCase().includes(query)) return true;
    return (img.workloads || []).some(w => `${w.kind}/${w.name} ${w.namespace}`.toLowerCase().includes(query));
  });
  const tbody = $("#cve-table-body");
  if (tbody) tbody.innerHTML = renderCVETableRows(filtered);
  const countSpan = $("#cve-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentCVEImages.length} images`;
}

function renderCVETableRows(images) {
  if (!images || images.length === 0) {
    return `<tr><td colspan="5" class="admin-empty-state">No matching container images running in cluster workloads.</td></tr>`;
  }
  return images.map((img, idx) => {
    const workloadsHtml = (img.workloads || []).slice(0, 3).map(w =>
      `<span class="admin-badge" style="font-size: 11px; margin-right: 4px; margin-bottom: 2px;">${escapeHTML(w.kind)}/${escapeHTML(w.name)} <span style="opacity: 0.6;">(${escapeHTML(w.namespace)})</span></span>`
    ).join("") + ((img.workloads || []).length > 3 ? `<span style="font-size: 11px; color: var(--admin-text-muted);">+${img.workloads.length - 3} more</span>` : "");

    let highestScore = 0;
    (img.vulnerabilities || []).forEach(v => {
      if (v.cvss_score > highestScore) highestScore = v.cvss_score;
    });

    const isClean = (img.total_cves || 0) === 0;

    return `
      <tr>
        <td class="admin-table-name">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span class="admin-badge ${isClean ? 'admin-badge-ready' : 'admin-badge-warning'}" style="font-size: 10px; font-weight: 700;">${isClean ? "CLEAN" : "WARN"}</span>
            <div>
              <strong style="font-family: monospace; font-size: 13px;">${escapeHTML(img.image)}</strong>
            </div>
          </div>
        </td>
        <td>${workloadsHtml || `<span style="color: var(--admin-text-muted); font-size: 12px;">Active Pod</span>`}</td>
        <td>
          <div style="display: flex; gap: 4px; align-items: center; flex-wrap: wrap;">
            ${img.critical_count > 0 ? `<span class="cve-badge-critical">${img.critical_count} CRITICAL</span>` : ""}
            ${img.high_count > 0 ? `<span class="cve-badge-high">${img.high_count} HIGH</span>` : ""}
            ${img.medium_count > 0 ? `<span class="cve-badge-medium">${img.medium_count} MED</span>` : ""}
            ${img.low_count > 0 ? `<span class="cve-badge-low">${img.low_count} LOW</span>` : ""}
            ${isClean ? `<span class="cve-badge-clean">CLEAN (0 CVEs)</span>` : ""}
          </div>
        </td>
        <td>
          ${highestScore > 0 ? `<span class="admin-badge ${highestScore >= 9.0 ? "admin-badge-danger" : highestScore >= 7.0 ? "admin-badge-warning" : "admin-badge-info"}">${highestScore.toFixed(1)}</span>` : `<span style="color: var(--admin-text-muted); font-size: 12px;">None</span>`}
        </td>
        <td style="text-align: right;">
          ${!isClean ? `<button class="admin-btn admin-btn-sm admin-btn-primary" onclick="openCVEModalByIndex(${idx})">Inspect CVEs (${img.total_cves})</button>` : `<span style="color: var(--admin-green); font-size: 12px; font-weight: 500;">Clean Image</span>`}
        </td>
      </tr>
    `;
  }).join("");
}

function openCVEModalByIndex(idx) {
  const item = currentCVEImages[idx];
  if (!item) return;
  const modal = $("#admin-cve-modal");
  $("#cve-modal-title").textContent = item.image;
  const badge = $("#cve-modal-badge");
  if (item.critical_count > 0) {
    badge.className = "admin-badge admin-badge-danger";
    badge.textContent = `${item.critical_count} CRITICAL`;
  } else if (item.high_count > 0) {
    badge.className = "admin-badge admin-badge-warning";
    badge.textContent = `${item.high_count} HIGH`;
  } else {
    badge.className = "admin-badge admin-badge-info";
    badge.textContent = `${item.total_cves} CVEs`;
  }

  const listHtml = (item.vulnerabilities || []).map(v => `
    <div style="border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); padding: 14px; margin-bottom: 12px; background: var(--admin-bg-secondary);">
      <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 8px;">
        <div>
          <span class="cve-badge-${(v.severity || "low").toLowerCase()}" style="margin-right: 8px;">${escapeHTML(v.severity)}</span>
          <strong style="font-size: 14px; font-family: monospace;">${escapeHTML(v.cve_id)}</strong>
          ${v.cvss_score ? `<span style="margin-left: 8px; font-size: 12px; color: var(--admin-text-muted);">CVSS: <strong>${v.cvss_score}</strong></span>` : ""}
        </div>
        <div style="font-size: 12px; font-family: monospace; color: var(--admin-text-muted);">
          pkg: <strong>${escapeHTML(v.package_name)}</strong> (${escapeHTML(v.installed_version)})
        </div>
      </div>
      <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">${escapeHTML(v.title)}</div>
      <p style="font-size: 12px; color: var(--admin-text); line-height: 1.5; margin: 0 0 10px;">${escapeHTML(v.description)}</p>
      <div style="background: rgba(50, 108, 229, 0.08); border-left: 3px solid var(--admin-accent); padding: 8px 12px; font-size: 12px; border-radius: 0 var(--admin-radius-sm) var(--admin-radius-sm) 0;">
        <strong>Remediation:</strong> ${escapeHTML(v.remediation)} ${v.fixed_version ? `(Fixed in version <code style="font-size: 11px;">${escapeHTML(v.fixed_version)}</code>)` : ""}
      </div>
    </div>
  `).join("");

  $("#cve-modal-body").innerHTML = listHtml;
  modal.classList.add("open");
}

// -------------------------------------------------------------
// Helm Release Lifecycle Manager
// -------------------------------------------------------------
let currentHelmReleases = [];
let currentHelmValues = null;
let activeHelmTab = "user";

async function renderHelmReleases(container) {
  container.innerHTML = `<div class="admin-empty-state">Discovering in-cluster Helm releases…</div>`;
  const ns = state.selectedNamespace;
  const url = ns ? `/api/v1/admin/helm/releases?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/helm/releases`;
  currentHelmReleases = await request(url);

  const deployedCount = currentHelmReleases.filter(r => (r.status || "").toLowerCase() === "deployed").length;
  const failedCount = currentHelmReleases.filter(r => (r.status || "").toLowerCase().includes("fail")).length;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Helm Releases &amp; Lifecycle Manager</h1>
        <p class="admin-view-subtitle">Discovery of Helm v3 releases, values inspection (values.yaml), rollback, and uninstallation.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('helm')">⌘K Search Helm</button>
        <button class="admin-btn admin-btn-primary" onclick="renderHelmReleases($('#admin-content-area'))">Refresh Releases</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Total Releases</span><span>Helm v3</span></div>
        <div class="admin-stat-value">${currentHelmReleases.length}</div>
        <div class="admin-stat-subtext">${ns ? `In namespace ${ns}` : "Across all cluster namespaces"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Deployed (Healthy)</span><span style="color: var(--admin-green);">Active</span></div>
        <div class="admin-stat-value" style="color: var(--admin-green);">${deployedCount}</div>
        <div class="admin-stat-subtext">Successfully applied charts</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Failed / Attention</span><span style="color: var(--admin-red);">Pending</span></div>
        <div class="admin-stat-value" style="color: ${failedCount > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${failedCount}</div>
        <div class="admin-stat-subtext">${failedCount === 0 ? "No broken Helm releases" : "Requires rollback or investigation"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Scope</span><span>Filter</span></div>
        <div class="admin-stat-value" style="font-size: 18px; text-transform: uppercase;">${escapeHTML(ns || "All Namespaces")}</div>
        <div class="admin-stat-subtext">Change via topbar dropdown</div>
      </div>
    </div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="helm-search-input" class="admin-input" placeholder="Filter by release name, chart, or status..." style="width: 320px;" oninput="filterHelmTable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="helm-table-count">${currentHelmReleases.length} releases discovered</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Release Name</th>
            <th>Namespace</th>
            <th>Revision</th>
            <th>Chart</th>
            <th>App Version</th>
            <th>Status</th>
            <th>Last Updated</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody id="helm-table-body">
          ${renderHelmTableRows(currentHelmReleases)}
        </tbody>
      </table>
    </div>
  `;
}

function filterHelmTable() {
  const query = ($("#helm-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentHelmReleases.filter(r =>
    r.name.toLowerCase().includes(query) ||
    r.namespace.toLowerCase().includes(query) ||
    (r.chart || "").toLowerCase().includes(query) ||
    (r.status || "").toLowerCase().includes(query)
  );
  const tbody = $("#helm-table-body");
  if (tbody) tbody.innerHTML = renderHelmTableRows(filtered);
  const countSpan = $("#helm-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentHelmReleases.length} releases`;
}

function renderHelmTableRows(releases) {
  if (!releases || releases.length === 0) {
    return `<tr><td colspan="8" class="admin-empty-state">No Helm v3 releases discovered in the target namespace.</td></tr>`;
  }
  return releases.map(r => {
    const isDeployed = (r.status || "").toLowerCase() === "deployed";
    return `
      <tr>
        <td class="admin-table-name">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span class="admin-badge" style="font-size: 10px; font-weight: 700;">HELM</span>
            <strong>${escapeHTML(r.name)}</strong>
          </div>
        </td>
        <td class="admin-table-ns">${escapeHTML(r.namespace)}</td>
        <td><span class="admin-badge" style="background: rgba(50,108,229,0.1); color: var(--admin-accent); font-weight: 600;">rev ${r.revision}</span></td>
        <td style="font-family: monospace; font-size: 12px;">${escapeHTML(r.chart || "—")}</td>
        <td style="font-family: monospace; font-size: 12px;">${escapeHTML(r.app_version || "—")}</td>
        <td>
          <span class="admin-badge ${isDeployed ? "admin-badge-ready" : "admin-badge-danger"}">${escapeHTML(r.status)}</span>
        </td>
        <td style="font-size: 12px; color: var(--admin-text-muted);">${r.updated ? escapeHTML(new Date(r.updated).toLocaleString()) : "—"}</td>
        <td style="text-align: right;">
          <div style="display: inline-flex; gap: 6px;">
            <button class="admin-btn admin-btn-sm" onclick="openHelmValuesModal('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}')">Values</button>
            <button class="admin-btn admin-btn-sm" onclick="rollbackHelmReleasePrompt('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}', ${r.revision})">Rollback</button>
            <button class="admin-btn admin-btn-sm admin-btn-danger" onclick="uninstallHelmReleasePrompt('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}')">Uninstall</button>
          </div>
        </td>
      </tr>
    `;
  }).join("");
}

async function openHelmValuesModal(namespace, name) {
  const modal = $("#admin-helm-modal");
  $("#helm-modal-title").textContent = `${name} (${namespace})`;
  $("#helm-modal-meta").textContent = "Loading values from secret…";
  $("#helm-values-code").textContent = "Fetching values.yaml…";
  modal.classList.add("open");

  try {
    const data = await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/values`);
    currentHelmValues = data;
    $("#helm-modal-meta").textContent = `Chart: ${data.chart} · Revision ${data.revision}`;
    switchHelmValuesTab("user");
  } catch (err) {
    $("#helm-values-code").textContent = `Failed to load Helm values: ${err.message}`;
  }
}

function switchHelmValuesTab(tab) {
  activeHelmTab = tab;
  $("#tab-btn-helm-user")?.classList.toggle("active", tab === "user");
  $("#tab-btn-helm-computed")?.classList.toggle("active", tab === "computed");

  if (!currentHelmValues) return;
  const codeEl = $("#helm-values-code");
  if (tab === "user") {
    codeEl.textContent = currentHelmValues.raw_yaml || objectToYAML(currentHelmValues.user_values || {});
  } else {
    codeEl.textContent = objectToYAML(currentHelmValues.chart_values || {});
  }
}

function copyHelmValuesToClipboard() {
  const text = $("#helm-values-code")?.textContent || "";
  navigator.clipboard.writeText(text).then(() => {
    alert("Helm values copied to clipboard!");
  }).catch(() => {
    alert("Could not copy values to clipboard.");
  });
}

async function rollbackHelmReleasePrompt(namespace, name, currentRev) {
  const targetRevStr = prompt(`Rollback Helm release '${name}' in namespace '${namespace}'\nCurrent revision: ${currentRev}\nEnter target revision number to roll back to:`, currentRev > 1 ? currentRev - 1 : 1);
  if (!targetRevStr) return;
  const targetRev = parseInt(targetRevStr.trim(), 10);
  if (isNaN(targetRev) || targetRev <= 0) {
    alert("Please enter a valid positive integer revision.");
    return;
  }
  try {
    await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/rollback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ revision: targetRev }),
    });
    alert(`Successfully rolled back release '${name}' to revision ${targetRev}!`);
    await renderHelmReleases($("#admin-content-area"));
  } catch (err) {
    alert(`Rollback failed: ${err.message}`);
  }
}

async function uninstallHelmReleasePrompt(namespace, name) {
  if (!confirm(`Are you sure you want to completely uninstall Helm release '${name}' in namespace '${namespace}'? This will delete all managed resources.`)) {
    return;
  }
  try {
    await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`, {
      method: "DELETE",
    });
    alert(`Successfully uninstalled release '${name}'.`);
    await renderHelmReleases($("#admin-content-area"));
  } catch (err) {
    alert(`Uninstall failed: ${err.message}`);
  }
}

// -------------------------------------------------------------
// Custom Resource Definitions (CRDs) & Operator Browser
// -------------------------------------------------------------
let currentCRDList = [];
let activeCRDInstances = null;

async function renderCRDs(container) {
  container.innerHTML = `<div class="admin-empty-state">Querying Custom Resource Definitions (apiextensions.k8s.io/v1)…</div>`;
  currentCRDList = await request("/api/v1/admin/crds");
  activeCRDInstances = null;

  const totalCRDs = currentCRDList.length;
  const groups = new Set(currentCRDList.map(c => c.group)).size;
  const namespacedCount = currentCRDList.filter(c => c.scope === "Namespaced").length;
  const clusterCount = currentCRDList.filter(c => c.scope === "Cluster").length;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Custom Resource Definitions (CRDs) &amp; Operator Browser</h1>
        <p class="admin-view-subtitle">CRD exploration, schema discovery, and real-time instance browsing for all installed Kubernetes operators.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('crd')">⌘K Search CRDs</button>
        <button class="admin-btn admin-btn-primary" onclick="renderCRDs($('#admin-content-area'))">Refresh CRDs</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Installed CRDs</span><span>Definitions</span></div>
        <div class="admin-stat-value">${totalCRDs}</div>
        <div class="admin-stat-subtext">Active custom resource schemas</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>API Groups</span><span>Vendors/Operators</span></div>
        <div class="admin-stat-value">${groups}</div>
        <div class="admin-stat-subtext">Unique API groups registered</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Namespaced CRDs</span><span>Multi-Tenant</span></div>
        <div class="admin-stat-value" style="color: var(--admin-accent);">${namespacedCount}</div>
        <div class="admin-stat-subtext">Scoped to individual namespaces</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cluster Scoped</span><span>Global</span></div>
        <div class="admin-stat-value">${clusterCount}</div>
        <div class="admin-stat-subtext">Cluster-wide custom resources</div>
      </div>
    </div>

    <div id="crd-instance-container"></div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="crd-search-input" class="admin-input" placeholder="Filter by kind, group, or resource name..." style="width: 320px;" oninput="filterCRDTable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="crd-table-count">${currentCRDList.length} CRDs registered</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Custom Resource / Kind</th>
            <th>API Group</th>
            <th>Version</th>
            <th>Scope</th>
            <th>Condition</th>
            <th>Instances</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody id="crd-table-body">
          ${renderCRDTableRows(currentCRDList)}
        </tbody>
      </table>
    </div>
  `;
}

function filterCRDTable() {
  const query = ($("#crd-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentCRDList.filter(c =>
    c.name.toLowerCase().includes(query) ||
    c.kind.toLowerCase().includes(query) ||
    c.group.toLowerCase().includes(query)
  );
  const tbody = $("#crd-table-body");
  if (tbody) tbody.innerHTML = renderCRDTableRows(filtered);
  const countSpan = $("#crd-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentCRDList.length} CRDs`;
}

function renderCRDTableRows(crds) {
  if (!crds || crds.length === 0) {
    return `<tr><td colspan="7" class="admin-empty-state">No matching Custom Resource Definitions found.</td></tr>`;
  }
  return crds.map(c => `
    <tr>
      <td class="admin-table-name">
        <div style="display: flex; align-items: center; gap: 8px;">
          <span class="admin-badge" style="font-size: 10px; font-weight: 700;">CRD</span>
          <div>
            <strong>${escapeHTML(c.kind)}</strong>
            <div style="font-size: 11px; font-family: monospace; color: var(--admin-text-muted);">${escapeHTML(c.plural)}</div>
          </div>
        </div>
      </td>
      <td style="font-family: monospace; font-size: 12px; color: var(--admin-accent);">${escapeHTML(c.group)}</td>
      <td><span class="admin-badge" style="font-size: 11px;">${escapeHTML(c.version)}</span></td>
      <td>
        <span class="admin-badge ${c.scope === "Namespaced" ? "admin-badge-info" : "admin-badge-warning"}">${escapeHTML(c.scope)}</span>
      </td>
      <td>
        <span class="admin-badge ${c.established ? "admin-badge-ready" : "admin-badge-danger"}">${c.established ? "Established" : "Not Ready"}</span>
      </td>
      <td style="font-weight: 600;">${c.instances_count !== undefined ? c.instances_count : "—"}</td>
      <td style="text-align: right;">
        <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="browseCRDInstances('${escapeHTML(c.group)}', '${escapeHTML(c.version)}', '${escapeHTML(c.plural)}', '${escapeHTML(c.kind)}')">Browse Instances</button>
      </td>
    </tr>
  `).join("");
}

async function browseCRDInstances(group, version, plural, kind) {
  const container = $("#crd-instance-container");
  if (!container) return;
  container.innerHTML = `
    <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-accent); border-radius: var(--admin-radius-sm); background: rgba(50,108,229,0.03);">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <h3 style="margin: 0; font-size: 15px;">Live Custom Resources: <span style="color: var(--admin-accent);">${escapeHTML(kind)}</span> <code style="font-size: 12px; opacity: 0.8;">(${escapeHTML(group)}/${escapeHTML(version)})</code></h3>
        <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close Panel</button>
      </div>
      <div class="admin-empty-state">Loading instances…</div>
    </div>
  `;
  container.scrollIntoView({ behavior: "smooth", block: "start" });

  try {
    const ns = state.selectedNamespace;
    const url = ns ? `/api/v1/admin/crds/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/crds/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}`;
    const data = await request(url);
    activeCRDInstances = data.items || [];

    if (activeCRDInstances.length === 0) {
      container.innerHTML = `
        <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); background: var(--admin-bg-secondary);">
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <h3 style="margin: 0; font-size: 15px;">No active instances found for <strong>${escapeHTML(kind)}</strong></h3>
            <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
          </div>
          <p style="margin: 8px 0 0; font-size: 12px; color: var(--admin-text-muted);">No custom resources exist in ${ns ? `namespace '${ns}'` : "the cluster"}.</p>
        </div>
      `;
      return;
    }

    container.innerHTML = `
      <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); background: var(--admin-bg);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
          <h3 style="margin: 0; font-size: 15px;">Instances of <strong>${escapeHTML(kind)}</strong> <span style="font-size: 12px; color: var(--admin-text-muted);">(${activeCRDInstances.length} objects)</span></h3>
          <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
        </div>
        <div class="admin-table-container">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Resource Name</th>
                <th>Namespace</th>
                <th>Created</th>
                <th style="text-align: right;">Action</th>
              </tr>
            </thead>
            <tbody>
              ${activeCRDInstances.map((item, idx) => `
                <tr>
                  <td class="admin-table-name"><strong>${escapeHTML(item.metadata?.name || "unnamed")}</strong></td>
                  <td class="admin-table-ns">${escapeHTML(item.metadata?.namespace || "Cluster-scoped")}</td>
                  <td style="font-size: 12px; color: var(--admin-text-muted);">${item.metadata?.creationTimestamp ? new Date(item.metadata.creationTimestamp).toLocaleString() : "—"}</td>
                  <td style="text-align: right;">
                    <button class="admin-btn admin-btn-sm" onclick="inspectCRDInstanceYAML(${idx})">View YAML</button>
                  </td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-red); border-radius: var(--admin-radius-sm); background: var(--admin-bg-secondary);">
        <div style="display: flex; justify-content: space-between; align-items: center;">
          <h3 style="margin: 0; font-size: 15px; color: var(--admin-red);">Failed to query ${escapeHTML(kind)} instances</h3>
          <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
        </div>
        <p style="margin: 8px 0 0; font-size: 12px; color: var(--admin-text);">${escapeHTML(err.message)}</p>
      </div>
    `;
  }
}

function inspectCRDInstanceYAML(idx) {
  const item = activeCRDInstances ? activeCRDInstances[idx] : null;
  if (!item) return;
  const modal = $("#admin-helm-modal");
  $("#helm-modal-title").textContent = `${item.kind || "CustomResource"}: ${item.metadata?.name || "object"}`;
  $("#helm-modal-meta").textContent = item.metadata?.namespace ? `Namespace: ${item.metadata.namespace}` : "Cluster-scoped";
  $("#tab-btn-helm-user").textContent = "YAML Definition";
  $("#tab-btn-helm-computed").textContent = "JSON Definition";
  $("#tab-btn-helm-user").classList.add("active");
  $("#tab-btn-helm-computed").classList.remove("active");
  $("#helm-values-code").textContent = objectToYAML(item);

  currentHelmValues = {
    raw_yaml: objectToYAML(item),
    chart_values: item,
  };
  modal.classList.add("open");
}

// -------------------------------------------------------------
// Interactive RBAC Permission Inspector (can-i)
// -------------------------------------------------------------
async function renderCanI(container) {
  const defaultNs = state.selectedNamespace || "default";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Interactive RBAC Permission Inspector (can-i)</h1>
        <p class="admin-view-subtitle">Visual &quot;Who Can Do What&quot; inspector. Evaluate Kubernetes RBAC authorizations across users, groups, and service accounts.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('can-i')">⌘K Quick Can-I</button>
        <button class="admin-btn admin-btn-primary" onclick="setRoute('rbac')">RBAC Blast Radius</button>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 340px; gap: 20px; margin-top: 10px;">
      <div class="admin-card" style="padding: 20px;">
        <h2 style="font-size: 15px; margin-top: 0; margin-bottom: 16px;">Test Authorization Policy</h2>
        <form id="can-i-form" onsubmit="handleCanISubmit(event)">
          <div class="admin-form-group" style="margin-bottom: 14px;">
            <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Subject / Identity</label>
            <input type="text" id="can-i-subject" class="admin-input" placeholder="e.g. system:serviceaccount:default:deploy-bot or leave blank for current admin user" style="width: 100%;">
            <div style="font-size: 11px; color: var(--admin-text-muted); margin-top: 4px;">Leave blank to evaluate permissions of your active cluster administrator session.</div>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 14px;">
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">API Verb</label>
              <select id="can-i-verb" class="admin-select" style="width: 100%;">
                <option value="get">get</option>
                <option value="list">list</option>
                <option value="watch">watch</option>
                <option value="create" selected>create</option>
                <option value="update">update</option>
                <option value="patch">patch</option>
                <option value="delete">delete</option>
                <option value="deletecollection">deletecollection</option>
                <option value="*">* (All Verbs)</option>
              </select>
            </div>
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Resource Kind</label>
              <input type="text" id="can-i-resource" class="admin-input" value="deployments" placeholder="e.g. pods, secrets, deployments, *" style="width: 100%;">
            </div>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 20px;">
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">API Group</label>
              <input type="text" id="can-i-group" class="admin-input" value="apps" placeholder="e.g. apps, batch, or blank for core" style="width: 100%;">
            </div>
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Namespace</label>
              <input type="text" id="can-i-namespace" class="admin-input" value="${escapeHTML(defaultNs)}" placeholder="e.g. default, production, or blank" style="width: 100%;">
            </div>
          </div>

          <button type="submit" class="admin-btn admin-btn-primary" style="width: 100%; padding: 10px; font-weight: 600;" id="can-i-submit-btn">
            Evaluate RBAC Access (can-i)
          </button>
        </form>

        <div id="can-i-result-area" style="margin-top: 20px;"></div>
      </div>

      <div class="admin-card" style="padding: 20px;">
        <h3 style="font-size: 14px; margin-top: 0; margin-bottom: 12px;">Quick RBAC Presets</h3>
        <p style="font-size: 12px; color: var(--admin-text-muted); line-height: 1.5; margin-bottom: 14px;">Click any security probe below to immediately run an RBAC policy evaluation:</p>
        
        <div style="display: flex; flex-direction: column; gap: 8px;">
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('', 'create', 'deployments', 'apps', 'default')">
            Can Current User create Deployments?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:serviceaccount:default:default', 'get', 'secrets', '', 'default')">
            Can default SA read Secrets in default?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:anonymous', 'list', 'pods', '', 'default')">
            Can Anonymous user list Pods?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:serviceaccount:crelan:builder', 'create', 'pods', '', 'crelan')">
            Can Platform Builder create Pods?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('developer', 'delete', 'namespaces', '', '')">
            Can Developer delete Namespaces?
          </button>
        </div>
      </div>
    </div>
  `;
}

function applyCanIPreset(subject, verb, resource, group, namespace) {
  $("#can-i-subject").value = subject;
  $("#can-i-verb").value = verb;
  $("#can-i-resource").value = resource;
  $("#can-i-group").value = group;
  $("#can-i-namespace").value = namespace;
  handleCanISubmit(new Event("submit"));
}

async function handleCanISubmit(e) {
  if (e && e.preventDefault) e.preventDefault();
  const subject = ($("#can-i-subject")?.value || "").trim();
  const verb = ($("#can-i-verb")?.value || "get").trim();
  const resource = ($("#can-i-resource")?.value || "pods").trim();
  const group = ($("#can-i-group")?.value || "").trim();
  const namespace = ($("#can-i-namespace")?.value || "").trim();

  const resultArea = $("#can-i-result-area");
  resultArea.innerHTML = `<div class="admin-empty-state">Evaluating RBAC permissions against cluster API...</div>`;

  try {
    const resp = await request("/api/v1/admin/security/can-i", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ subject, verb, resource, group, namespace }),
    });

    const isAllowed = resp.allowed === true;
    resultArea.innerHTML = `
      <div style="border-radius: var(--admin-radius-sm); border: 2px solid ${isAllowed ? "var(--admin-green)" : "var(--admin-red)"}; background: ${isAllowed ? "rgba(16,185,129,0.06)" : "rgba(239,68,68,0.06)"}; padding: 18px; margin-top: 12px;">
        <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px;">
          <div style="display: flex; align-items: center; gap: 10px;">
            
            <strong style="font-size: 18px; color: ${isAllowed ? "var(--admin-green)" : "var(--admin-red)"}; letter-spacing: 0.5px;">${isAllowed ? "ALLOWED" : "DENIED"}</strong>
          </div>
          <span class="admin-badge" style="font-size: 11px;">${escapeHTML(resp.evaluation_mode || "RBAC Engine")}</span>
        </div>
        <p style="font-size: 13px; line-height: 1.5; color: var(--admin-text); margin: 6px 0 10px;">
          ${escapeHTML(resp.reason || (isAllowed ? "Authorization granted by cluster RBAC policy rules." : "No RoleBinding or ClusterRoleBinding grants this operation."))}
        </p>
        <div style="font-size: 11px; font-family: monospace; color: var(--admin-text-muted); background: var(--admin-bg); padding: 8px; border-radius: var(--admin-radius-sm); border: 1px solid var(--admin-border);">
          Verb: <strong>${escapeHTML(verb)}</strong> · Resource: <strong>${escapeHTML(resource)}</strong> · Group: <strong>${escapeHTML(group || "core")}</strong> · Namespace: <strong>${escapeHTML(namespace || "(cluster-wide)")}</strong>
        </div>
      </div>
    `;
  } catch (err) {
    resultArea.innerHTML = `
      <div style="border-radius: var(--admin-radius-sm); border: 1px solid var(--admin-red); background: var(--admin-bg-secondary); padding: 14px; margin-top: 12px; color: var(--admin-red);">
        <strong>Check Failed:</strong> ${escapeHTML(err.message)}
      </div>
    `;
  }
}

// -------------------------------------------------------------
// Multi-Cluster Fleet Dashboard (ClickUp-Style List & Grid)
// -------------------------------------------------------------

const CLICKUP_PRESET_COLORS = [
  "#ef4444", "#f43f5e", "#f97316", "#f59e0b",
  "#10b981", "#14b8a6", "#06b6d4", "#3b82f6",
  "#6366f1", "#8b5cf6", "#d946ef", "#64748b"
];

let selectedClusterCustomContext = null;
let selectedClusterCustomColor = "#3b82f6";

function setFleetViewMode(mode) {
  state.fleetViewMode = mode;
  localStorage.setItem("crelan_fleet_view_mode", mode);
  const container = $("#admin-content-area");
  if (container) renderClusterFleet(container);
}

async function renderClusterFleet(container) {
  container.innerHTML = `<div class="admin-empty-state">Loading cluster fleet status…</div>`;
  const fleet = await request("/api/v1/admin/clusters/fleet");
  state.fleet = fleet;

  const totalClusters = fleet.length;
  if (!fleet || fleet.length === 0) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Multi-Cluster Fleet</h1>
          <p class="admin-view-subtitle">Centralized multi-cluster fleet management with instant context switching, custom environment palettes, and live health telemetry.</p>
        </div>
        <div style="display: flex; align-items: center; gap: 12px;">
          <a class="admin-btn admin-btn-primary" href="#cluster-connect">+ Connect Cluster</a>
        </div>
      </div>
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 48px 20px;">
          <h3>No Clusters Connected</h3>
          <p style="color: var(--admin-text-muted); max-width: 480px; margin: 8px auto 20px;">
            No Kubernetes cluster contexts were found in your environment or storage. Connect a cluster via kubeconfig or environment variables to start managing your fleet.
          </p>
          <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Your First Cluster</a>
        </div>
      </div>
    `;
    return;
  }

  const currentCluster = fleet.find(c => c.is_current || c.is_active);
  const totalNodes = fleet.reduce((acc, c) => acc + (c.nodes_count || c.node_count || 0), 0);
  const totalPods = fleet.reduce((acc, c) => acc + (c.total_pods || 0), 0);

  const viewMode = state.fleetViewMode || localStorage.getItem("crelan_fleet_view_mode") || "grid";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <div style="display: flex; align-items: center; gap: 10px;">
          <h1>Multi-Cluster Fleet</h1>
          <span class="admin-badge admin-badge-ready">${totalClusters} ${totalClusters === 1 ? 'Cluster' : 'Clusters'}</span>
        </div>
        <p class="admin-view-subtitle">Centralized multi-cluster fleet management with instant context switching, custom environment palettes, and live health telemetry.</p>
      </div>
      <div style="display: flex; align-items: center; gap: 12px;">
        <div class="clickup-view-toggle">
          <button class="clickup-view-btn ${viewMode === 'grid' ? 'active' : ''}" type="button" onclick="setFleetViewMode('grid')">
            <span>⊞</span> Grid View
          </button>
          <button class="clickup-view-btn ${viewMode === 'list' ? 'active' : ''}" type="button" onclick="setFleetViewMode('list')">
            List View
          </button>
        </div>
        <a class="admin-btn admin-btn-primary" href="#cluster-connect">+ Connect Cluster</a>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Managed Clusters</span><span>Fleet Size</span></div>
        <div class="admin-stat-value">${totalClusters}</div>
        <div class="admin-stat-subtext">Configured cluster environments</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Context</span><span style="color: var(--admin-green);">Connected</span></div>
        <div class="admin-stat-value" style="font-size: 18px; font-family: monospace; display: flex; align-items: center; gap: 8px;">
          <span style="width: 10px; height: 10px; border-radius: 50%; background-color: ${escapeHTML(currentCluster ? (currentCluster.color || '#3b82f6') : '#94a3b8')}; display: inline-block;"></span>
          ${escapeHTML(currentCluster ? (currentCluster.alias || currentCluster.name) : "None")}
        </div>
        <div class="admin-stat-subtext">${currentCluster ? (currentCluster.server_version || currentCluster.version || "Active") : "Disconnected"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Fleet Capacity</span><span>Nodes</span></div>
        <div class="admin-stat-value">${totalNodes}</div>
        <div class="admin-stat-subtext">Total worker & control nodes</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Pods</span><span>Workloads</span></div>
        <div class="admin-stat-value" style="color: var(--admin-green);">${totalPods > 0 ? totalPods : (currentCluster ? currentCluster.total_pods || 0 : 0)}</div>
        <div class="admin-stat-subtext">Running across fleet</div>
      </div>
    </div>

    <div style="display: flex; align-items: center; justify-content: space-between; margin: 28px 0 14px;">
      <h2 style="font-size: 15px; font-weight: 700; margin: 0;">Connected Environments (${totalClusters})</h2>
      <div style="font-size: 12px; color: var(--admin-text-muted);">
        Tip: Click any cluster icon or swatch to customize its background color.
      </div>
    </div>

    ${viewMode === "grid" ? `
      <!-- ClickUp-Style Grid View -->
      <div style="display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); gap: 18px;">
        ${fleet.map(c => {
          const isActive = Boolean(c.is_current || c.is_active);
          const color = c.color || "#3b82f6";
          const monogram = getClusterMonogram(c.name, c.alias);
          const env = (c.environment || "production").toLowerCase();
          return `
            <div class="fleet-grid-card ${isActive ? 'is-active' : ''}">
              <div class="fleet-card-color-stripe" style="background-color: ${escapeHTML(color)};"></div>
              <div class="fleet-card-body">
                <div class="fleet-card-header">
                  <div style="display: flex; align-items: center; gap: 12px;">
                    <div class="fleet-cluster-badge" style="background-color: ${escapeHTML(color)};">
                      ${escapeHTML(monogram)}
                    </div>
                    <div>
                      <h3 style="margin: 0; font-size: 16px; font-weight: 700; display: flex; align-items: center; gap: 8px;">
                        ${escapeHTML(c.alias || c.name)}
                      </h3>
                      <div style="display: flex; align-items: center; gap: 6px; margin-top: 3px;">
                        <span class="fleet-provider-badge">${escapeHTML(c.provider || "Kubernetes")}</span>
                        <span class="fleet-card-env-badge env-${escapeHTML(env)}">${escapeHTML(c.environment || "Production")}</span>
                      </div>
                    </div>
                  </div>
                  <div>
                    ${isActive ? `<span class="admin-badge admin-badge-ready">ACTIVE</span>` : ""}
                  </div>
                </div>

                <div style="font-size: 11px; color: var(--admin-text-muted); font-family: monospace; word-break: break-all; margin-bottom: 12px; background: #f8fafc; padding: 6px 10px; border-radius: 6px; border: 1px solid #edf2f7;">
                  context: <strong>${escapeHTML(c.name)}</strong>
                </div>

                <div class="fleet-metrics-grid">
                  <div class="fleet-metric-item">
                    <span class="fleet-metric-label">Nodes</span>
                    <span class="fleet-metric-val">${c.nodes_count ?? c.node_count ?? 0}</span>
                  </div>
                  <div class="fleet-metric-item">
                    <span class="fleet-metric-label">Pods</span>
                    <span class="fleet-metric-val">${c.total_pods ?? 0}</span>
                  </div>
                  <div class="fleet-metric-item">
                    <span class="fleet-metric-label">Version</span>
                    <span class="fleet-metric-val" style="font-size: 12px; font-family: monospace;">${escapeHTML(c.server_version || c.version || "v1.28+")}</span>
                  </div>
                </div>

                <div style="display: flex; gap: 8px; margin-top: 4px;">
                  ${isActive ? `
                    <button class="admin-btn admin-btn-sm" style="flex: 1; background: #ecfdf5; color: #059669; border-color: #a7f3d0; cursor: default;" disabled>
                      Active Cluster
                    </button>
                  ` : `
                    <button class="admin-btn admin-btn-primary admin-btn-sm" style="flex: 1;" onclick="switchClusterContext('${escapeHTML(c.name)}')">
                      Switch Context
                    </button>
                    <button class="admin-btn admin-btn-sm" type="button" title="Disconnect cluster from fleet" onclick="disconnectClusterContext('${escapeHTML(c.name)}')">
                      &times;
                    </button>
                  `}
                  <button class="admin-btn admin-btn-sm" type="button" title="Customize Cluster Color & Name" onclick="openClusterColorModal('${escapeHTML(c.name)}')">
                    Color
                  </button>
                </div>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    ` : `
      <!-- ClickUp-Style List View -->
      <div class="admin-table-container">
        <table class="clickup-fleet-table">
          <thead>
            <tr>
              <th>Cluster / Context</th>
              <th style="width: 60px; text-align: center;">Color</th>
              <th>Environment</th>
              <th>Status</th>
              <th>Provider</th>
              <th style="text-align: right;">Nodes</th>
              <th style="text-align: right;">Pods</th>
              <th>K8s Version</th>
              <th style="text-align: right; width: 140px;">Actions</th>
            </tr>
          </thead>
          <tbody>
            ${fleet.map(c => {
              const isActive = Boolean(c.is_current || c.is_active);
              const color = c.color || "#3b82f6";
              const monogram = getClusterMonogram(c.name, c.alias);
              const env = (c.environment || "production").toLowerCase();
              return `
                <tr class="${isActive ? 'row-active' : ''}">
                  <td>
                    <div style="display: flex; align-items: center; gap: 10px;">
                      <div class="fleet-cluster-badge" style="width: 28px; height: 28px; font-size: 11px; background-color: ${escapeHTML(color)};">
                        ${escapeHTML(monogram)}
                      </div>
                      <div>
                        <strong style="display: block; font-size: 13px;">${escapeHTML(c.alias || c.name)}</strong>
                        ${c.alias ? `<span style="font-size: 11px; color: var(--admin-text-muted); font-family: monospace;">${escapeHTML(c.name)}</span>` : ""}
                      </div>
                    </div>
                  </td>
                  <td style="text-align: center;">
                    <span class="clickup-color-dot" style="background-color: ${escapeHTML(color)};" title="Click to customize cluster color" onclick="openClusterColorModal('${escapeHTML(c.name)}')"></span>
                  </td>
                  <td>
                    <span class="fleet-card-env-badge env-${escapeHTML(env)}">
                      ${escapeHTML(c.environment || "Production")}
                    </span>
                  </td>
                  <td>
                    <span style="display: inline-flex; align-items: center; gap: 6px; font-weight: 500;">
                      <span class="status-dot" style="background-color: ${isActive ? 'var(--admin-green)' : '#94a3b8'};"></span>
                      ${isActive ? 'Active' : 'Connected'}
                    </span>
                  </td>
                  <td>
                    <span class="fleet-provider-badge">${escapeHTML(c.provider || "Kubernetes")}</span>
                  </td>
                  <td style="text-align: right; font-weight: 600;">${c.nodes_count ?? c.node_count ?? 0}</td>
                  <td style="text-align: right; font-weight: 600;">${c.total_pods ?? 0}</td>
                  <td>
                    <span style="font-family: monospace; font-size: 12px; color: var(--admin-text-muted);">${escapeHTML(c.server_version || c.version || "v1.28+")}</span>
                  </td>
                  <td style="text-align: right;">
                    <div style="display: inline-flex; gap: 6px;">
                      ${isActive ? `
                        <span class="admin-badge admin-badge-ready" style="font-size: 11px;">Active</span>
                      ` : `
                        <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="switchClusterContext('${escapeHTML(c.name)}')">Switch</button>
                        <button class="admin-btn admin-btn-sm" title="Disconnect Cluster" onclick="disconnectClusterContext('${escapeHTML(c.name)}')">&times;</button>
                      `}
                      <button class="admin-btn admin-btn-sm" title="Customize Cluster" onclick="openClusterColorModal('${escapeHTML(c.name)}')">Color</button>
                    </div>
                  </td>
                </tr>
              `;
            }).join("")}
          </tbody>
        </table>
      </div>
    `}
  `;
}

async function switchClusterContext(contextName) {
  try {
    await request("/api/v1/admin/clusters/switch-context", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ context: contextName }),
    });
    await loadClusterHeader();
    await loadClusterFleetRail();
    if (state.activeView === "cluster-fleet") {
      await renderClusterFleet($("#admin-content-area"));
    } else {
      await renderCurrentView();
    }
  } catch (err) {
    alert(`Failed to switch context: ${err.message}`);
  }
}

async function disconnectClusterContext(contextName) {
  if (!confirm(`Are you sure you want to disconnect cluster "${contextName}" from the fleet?`)) return;
  try {
    await request("/api/v1/admin/clusters/disconnect", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ context: contextName }),
    });
    await loadClusterHeader();
    await loadClusterFleetRail();
    if (state.activeView === "cluster-fleet") {
      await renderClusterFleet($("#admin-content-area"));
    } else {
      await renderCurrentView();
    }
  } catch (err) {
    alert(`Failed to disconnect cluster: ${err.message}`);
  }
}

function openClusterColorModal(contextName) {
  const fleet = state.fleet || [];
  const cluster = fleet.find(c => c.name === contextName) || { name: contextName, color: "#3b82f6", alias: "", environment: "production" };
  selectedClusterCustomContext = contextName;
  selectedClusterCustomColor = cluster.color || "#3b82f6";

  const modal = $("#cluster-custom-modal");
  if (!modal) return;

  $("#modal-cluster-context-name").textContent = cluster.name;
  $("#modal-cluster-alias").value = cluster.alias && cluster.alias !== cluster.name ? cluster.alias : "";
  $("#modal-cluster-env").value = (cluster.environment || "production").toLowerCase();
  $("#modal-cluster-color-hex").value = selectedClusterCustomColor;
  $("#modal-cluster-color-native").value = selectedClusterCustomColor;

  updateModalClusterPreview();
  renderClickUpColorPalette();

  modal.style.display = "flex";
}

function closeClusterColorModal() {
  const modal = $("#cluster-custom-modal");
  if (modal) modal.style.display = "none";
}

function updateModalClusterPreview() {
  const alias = $("#modal-cluster-alias")?.value || "";
  const preview = $("#modal-cluster-preview");
  if (preview) {
    preview.style.backgroundColor = selectedClusterCustomColor;
    preview.textContent = getClusterMonogram(selectedClusterCustomContext || "K8", alias);
  }
}

function renderClickUpColorPalette() {
  const container = $("#modal-cluster-palette");
  if (!container) return;
  container.innerHTML = CLICKUP_PRESET_COLORS.map(color => {
    const isSelected = (selectedClusterCustomColor.toLowerCase() === color.toLowerCase());
    return `
      <div class="clickup-palette-swatch ${isSelected ? 'selected' : ''}"
        style="background-color: ${color};"
        title="${color}"
        onclick="selectClusterCustomColor('${color}')">
      </div>
    `;
  }).join("");
}

function selectClusterCustomColor(color) {
  selectedClusterCustomColor = color;
  const hexInput = $("#modal-cluster-color-hex");
  const natInput = $("#modal-cluster-color-native");
  if (hexInput) hexInput.value = color;
  if (natInput) natInput.value = color;
  updateModalClusterPreview();
  renderClickUpColorPalette();
}

async function saveClusterCustomization() {
  if (!selectedClusterCustomContext) return;
  const alias = ($("#modal-cluster-alias")?.value || "").trim();
  const env = $("#modal-cluster-env")?.value || "production";
  const color = selectedClusterCustomColor;

  try {
    await request("/api/v1/admin/clusters/customizations", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        context_name: selectedClusterCustomContext,
        color: color,
        alias: alias,
        environment: env
      })
    });
    closeClusterColorModal();
    await loadClusterHeader();
    if (state.activeView === "cluster-fleet") {
      await renderClusterFleet($("#admin-content-area"));
    }
  } catch (err) {
    alert(`Failed to save cluster customization: ${err.message}`);
  }
}
// -------------------------------------------------------------
// Site Reliability Engineering (SRE) Operations Suite
// -------------------------------------------------------------

// 1. SLO & Error Budget Burn Radar
async function renderSRESLOs(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/slos?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/slos`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>SLO & Error Budget Burn Radar</h1>
        <p>Real-time Service Level Objectives, error budget exhaustion dials, and automated deployment freeze gates.</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary admin-btn-sm" type="button" onclick="openCreateSLOModal()">+ Define New SLO</button>
      </div>
    </div>
    <div class="admin-empty-state">Calculating SLO telemetry and burn velocity…</div>
  `;

  try {
    const res = await request(url);
    const slosRaw = (res && Array.isArray(res.slos)) ? res.slos : (Array.isArray(res) ? res : []);
    if (!slosRaw || slosRaw.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>SLO & Error Budget Burn Radar</h1>
            <p>Real-time Service Level Objectives, error budget exhaustion dials, and automated deployment freeze gates.</p>
          </div>
          <div class="admin-view-actions">
            <button class="admin-btn admin-btn-primary admin-btn-sm" type="button" onclick="openCreateSLOModal()">+ Define New SLO</button>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Service Level Objectives Configured</h3>
            <p style="color: var(--admin-text-muted); margin: 8px 0 16px;">
              Define availability or latency SLOs for critical microservices to monitor error budget burn and protect production stability.
            </p>
            <button class="admin-btn admin-btn-primary" type="button" onclick="openCreateSLOModal()">Define Your First SLO</button>
          </div>
        </div>
      `;
      return;
    }

    const slos = slosRaw.map(s => ({
      ...s,
      service: s.service || s.service_name || "-",
      target_percentage: Number(s.target_percentage !== undefined ? s.target_percentage : (s.target_sli !== undefined ? s.target_sli : 99.9)),
      current_percentage: Number(s.current_percentage !== undefined ? s.current_percentage : (s.current_sli !== undefined ? s.current_sli : 100)),
      remaining_budget_pct: Number(s.remaining_budget_pct !== undefined ? s.remaining_budget_pct : (s.error_budget_remaining !== undefined ? s.error_budget_remaining : 100)),
      burn_velocity_1h: Number(s.burn_velocity_1h !== undefined ? s.burn_velocity_1h : (s.burn_rate_1h !== undefined ? s.burn_rate_1h : 0)),
      burn_velocity_6h: Number(s.burn_velocity_6h !== undefined ? s.burn_velocity_6h : (s.burn_rate_6h !== undefined ? s.burn_rate_6h : 0)),
      status: s.status || (s.current_sli >= s.target_sli ? "met" : "burning"),
      deployment_frozen: Boolean(s.deployment_frozen !== undefined ? s.deployment_frozen : (s.deployment_freeze !== undefined ? s.deployment_freeze : false))
    }));

    const total = slos.length;
    const met = slos.filter(s => s.status === "met").length;
    const burning = slos.filter(s => s.burn_velocity_1h > 1.0).length;
    const frozen = slos.filter(s => s.deployment_frozen).length;

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>SLO & Error Budget Burn Radar</h1>
          <p>Real-time Service Level Objectives, error budget exhaustion dials, and automated deployment freeze gates.</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-primary admin-btn-sm" type="button" onclick="openCreateSLOModal()">+ Define New SLO</button>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Active Objectives</div>
          <div class="sre-kpi-val">${total}</div>
          <div class="sre-kpi-meta">${met} of ${total} objectives met</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Burn Velocity Alert</div>
          <div class="sre-kpi-val" style="color: ${burning > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${burning}</div>
          <div class="sre-kpi-meta">&gt; 1x budget burn rate</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Deployment Freezes</div>
          <div class="sre-kpi-val" style="color: ${frozen > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${frozen}</div>
          <div class="sre-kpi-meta">${frozen > 0 ? "Deployment guard active" : "All pipelines clear"}</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Avg Budget Remaining</div>
          <div class="sre-kpi-val">${(slos.reduce((a, b) => a + b.remaining_budget_pct, 0) / total).toFixed(1)}%</div>
          <div class="sre-kpi-meta">Across 30d rolling window</div>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>SLO Name</th>
              <th>Service</th>
              <th>Target</th>
              <th>Current SLI</th>
              <th style="min-width: 140px;">Error Budget Remaining</th>
              <th>Burn Velocity (1h / 6h)</th>
              <th>Status</th>
              <th>Deployment Guard</th>
              <th style="text-align: right;">Actions</th>
            </tr>
          </thead>
          <tbody>
            ${slos.map(s => {
              const fillClass = s.remaining_budget_pct < 20 ? "sre-burn-critical" : s.remaining_budget_pct < 50 ? "sre-burn-warn" : "sre-burn-safe";
              return `
                <tr>
                  <td><strong>${escapeHTML(s.name)}</strong></td>
                  <td>
                    <span class="admin-kind-badge">SVC</span>
                    <code>${escapeHTML(s.namespace)}/${escapeHTML(s.service)}</code>
                  </td>
                  <td>${s.target_percentage.toFixed(2)}%</td>
                  <td><strong style="color: ${s.current_percentage >= s.target_percentage ? "var(--admin-green)" : "var(--admin-red)"};">${s.current_percentage.toFixed(2)}%</strong></td>
                  <td>
                    <div style="font-weight: 700; font-size: 11px;">${s.remaining_budget_pct.toFixed(1)}%</div>
                    <div class="sre-burn-bar-wrap">
                      <div class="sre-burn-bar-fill ${fillClass}" style="width: ${Math.max(0, Math.min(100, s.remaining_budget_pct))}%;"></div>
                    </div>
                  </td>
                  <td>
                    <span class="admin-badge ${s.burn_velocity_1h >= 2 ? "admin-badge-danger" : s.burn_velocity_1h >= 1 ? "admin-badge-warning" : "admin-badge-success"}">
                      ${s.burn_velocity_1h.toFixed(1)}x / ${s.burn_velocity_6h.toFixed(1)}x
                    </span>
                  </td>
                  <td>
                    <span class="admin-badge ${s.status === "met" ? "admin-badge-success" : s.status === "exhausted" ? "admin-badge-danger" : "admin-badge-warning"}">
                      ${escapeHTML(s.status.toUpperCase())}
                    </span>
                  </td>
                  <td>
                    ${s.deployment_frozen ? `
                      <span class="admin-badge admin-badge-danger">FROZEN</span>
                    ` : `
                      <span class="admin-badge admin-badge-success">ALLOWED</span>
                    `}
                  </td>
                  <td style="text-align: right;">
                    <button class="admin-btn admin-btn-sm admin-btn-danger" type="button" onclick="deleteSRESLO('${escapeHTML(s.id)}')">Delete</button>
                  </td>
                </tr>
              `;
            }).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to load SLOs: ${escapeHTML(err.message)}</div>`;
  }
}

function openCreateSLOModal() {
  const modal = $("#admin-slo-modal");
  if (!modal) return;
  modal.classList.add("open");
  $("#slo-namespace-input").value = state.selectedNamespace || "default";
  $("#slo-name-input").focus();
}

async function handleCreateSLO(e) {
  e.preventDefault();
  const name = $("#slo-name-input").value.trim();
  const service = $("#slo-service-input").value.trim();
  const ns = $("#slo-namespace-input").value.trim();
  const target = parseFloat($("#slo-target-input").value) || 99.9;
  const window = $("#slo-window-select").value;
  const indicator = $("#slo-indicator-select").value;
  const threshold = parseFloat($("#slo-latency-threshold-input").value) || 250;
  const channel = $("#slo-channel-input").value.trim();

  try {
    await request("/api/v1/admin/sre/slos", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        service,
        namespace: ns,
        target_percentage: target,
        window,
        indicator_type: indicator,
        latency_threshold_ms: threshold,
        alert_channel: channel,
      }),
    });
    $("#admin-slo-modal").classList.remove("open");
    $("#slo-form").reset();
    renderCurrentView();
  } catch (err) {
    alert("Failed to create SLO: " + err.message);
  }
}

async function deleteSRESLO(id) {
  if (!confirm("Are you sure you want to delete this SLO definition?")) return;
  try {
    await request(`/api/v1/admin/sre/slos?id=${encodeURIComponent(id)}`, { method: "DELETE" });
    renderCurrentView();
  } catch (err) {
    alert("Failed to delete SLO: " + err.message);
  }
}

// 2. The 4 Golden Signals & RED Engine
async function renderSREGoldenSignals(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/golden-signals?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/golden-signals`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>The 4 Golden Signals & RED Engine</h1>
        <p>Google SRE Rate, Errors, Duration (P50/P90/P99 latency), and Saturation across cluster microservices.</p>
      </div>
    </div>
    <div class="admin-empty-state">Aggregating real-time RED signals…</div>
  `;

  try {
    const res = await request(url);
    const signalsRaw = (res && Array.isArray(res.signals)) ? res.signals : (Array.isArray(res) ? res : []);
    if (!signalsRaw || signalsRaw.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>The 4 Golden Signals & RED Engine</h1>
            <p>Google SRE Rate, Errors, Duration (P50/P90/P99 latency), and Saturation across cluster microservices.</p>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Active Microservices Found</h3>
            <p style="color: var(--admin-text-muted);">Ensure deployments and services are running in the target namespace.</p>
          </div>
        </div>
      `;
      return;
    }

    const signals = signalsRaw.map(s => ({
      ...s,
      service: s.service || s.service_name || "-",
      rate_rps: Number(s.rate_rps !== undefined ? s.rate_rps : 0),
      error_rate_pct: Number(s.error_rate_pct !== undefined ? s.error_rate_pct : 0),
      duration_p99_ms: Number(s.duration_p99_ms !== undefined ? s.duration_p99_ms : (s.latency_p99_ms !== undefined ? s.latency_p99_ms : 0)),
      duration_p50_ms: Number(s.duration_p50_ms !== undefined ? s.duration_p50_ms : (s.latency_p50_ms !== undefined ? s.latency_p50_ms : 0)),
      cpu_saturation_pct: Number(s.cpu_saturation_pct !== undefined ? s.cpu_saturation_pct : 0),
      memory_saturation_pct: Number(s.memory_saturation_pct !== undefined ? s.memory_saturation_pct : 0),
      saturation_cpu_pct: Number(s.saturation_cpu_pct !== undefined ? s.saturation_cpu_pct : (s.cpu_saturation_pct !== undefined ? s.cpu_saturation_pct : 0)),
      saturation_mem_pct: Number(s.saturation_mem_pct !== undefined ? s.saturation_mem_pct : (s.memory_saturation_pct !== undefined ? s.memory_saturation_pct : 0)),
    }));

    const totalRPS = signals.reduce((acc, s) => acc + s.rate_rps, 0);
    const avgErrorPct = (signals.reduce((acc, s) => acc + s.error_rate_pct, 0) / (signals.length || 1));
    const degradedCount = signals.filter(s => s.error_rate_pct > 1.0 || s.duration_p99_ms > 500).length;

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>The 4 Golden Signals & RED Engine</h1>
          <p>Google SRE Rate, Errors, Duration (P50/P90/P99 latency), and Saturation across cluster microservices.</p>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Monitored Services</div>
          <div class="sre-kpi-val">${signals.length}</div>
          <div class="sre-kpi-meta">Real-time HTTP/gRPC ingress</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Total Cluster Throughput</div>
          <div class="sre-kpi-val">${totalRPS.toFixed(1)} <span style="font-size: 14px; font-weight: 500;">req/s</span></div>
          <div class="sre-kpi-meta">Aggregated traffic volume</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Mean Cluster Error Rate</div>
          <div class="sre-kpi-val" style="color: ${avgErrorPct > 1.0 ? "var(--admin-red)" : "var(--admin-green)"};">${avgErrorPct.toFixed(2)}%</div>
          <div class="sre-kpi-meta">5xx and connection aborts</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Degraded Services</div>
          <div class="sre-kpi-val" style="color: ${degradedCount > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${degradedCount}</div>
          <div class="sre-kpi-meta">Latency / Error threshold breached</div>
        </div>
      </div>

      <div style="display: flex; flex-direction: column; gap: 14px;">
        ${signals.map(s => {
          const isDegraded = s.error_rate_pct > 1.0 || s.duration_p99_ms > 500;
          return `
            <div class="sre-signal-card">
              <div class="sre-signal-header">
                <div style="display: flex; align-items: center; gap: 10px;">
                  <span class="admin-kind-badge">SVC</span>
                  <strong style="font-size: 14px;">${escapeHTML(s.service)}</strong>
                  <span style="font-size: 12px; color: var(--admin-text-muted);">${escapeHTML(s.namespace)}</span>
                </div>
                <div>
                  ${isDegraded ? `
                    <span class="admin-badge admin-badge-danger">DEGRADED</span>
                  ` : `
                    <span class="admin-badge admin-badge-success">HEALTHY</span>
                  `}
                </div>
              </div>

              <div class="sre-signal-grid">
                <div class="sre-signal-metric">
                  <div class="sre-signal-metric-label">Rate (Throughput)</div>
                  <div class="sre-signal-metric-value">${s.rate_rps.toFixed(1)} <span style="font-size: 11px; font-weight: normal; color: var(--admin-text-muted);">req/s</span></div>
                </div>

                <div class="sre-signal-metric">
                  <div class="sre-signal-metric-label">Errors (5xx Ratio)</div>
                  <div class="sre-signal-metric-value" style="color: ${s.error_rate_pct > 1.0 ? "var(--admin-red)" : "var(--admin-text)"};">
                    ${s.error_rate_pct.toFixed(2)}%
                  </div>
                </div>

                <div class="sre-signal-metric">
                  <div class="sre-signal-metric-label">Duration Latency</div>
                  <div class="sre-signal-metric-value">
                    <span title="P50">${Math.round(s.duration_p50_ms)}ms</span>
                    <span style="font-size: 11px; color: var(--admin-text-muted);"> (P99: ${Math.round(s.duration_p99_ms)}ms)</span>
                  </div>
                </div>

                <div class="sre-signal-metric">
                  <div class="sre-signal-metric-label">Saturation (CPU/Mem)</div>
                  <div class="sre-signal-metric-value">
                    ${Math.round(s.saturation_cpu_pct)}% <span style="font-size: 11px; color: var(--admin-text-muted);">CPU</span>
                    / ${Math.round(s.saturation_mem_pct)}% <span style="font-size: 11px; color: var(--admin-text-muted);">Mem</span>
                  </div>
                </div>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to load Golden Signals: ${escapeHTML(err.message)}</div>`;
  }
}

// 3. Health Probe Doctor
async function renderSREProbes(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/probe-audit?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/probe-audit`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Health Probe Doctor & Anti-Patterns</h1>
        <p>Identify missing liveness/readiness probes, cascading timeouts, startup probe gaps, and deep-check failure traps.</p>
      </div>
    </div>
    <div class="admin-empty-state">Auditing pod probe configurations…</div>
  `;

  try {
    const res = await request(url);
    const probes = (res && Array.isArray(res.issues)) ? res.issues : (Array.isArray(res) ? res : []);
    if (!probes || probes.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>Health Probe Doctor & Anti-Patterns</h1>
            <p>Identify missing liveness/readiness probes, cascading timeouts, startup probe gaps, and deep-check failure traps.</p>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Deployments Found to Audit</h3>
            <p style="color: var(--admin-text-muted);">Deploy microservices to perform health probe validation.</p>
          </div>
        </div>
      `;
      return;
    }

    const criticalCount = probes.filter(p => p.risk_level === "critical" || p.risk_level === "high").length;
    const missingLive = probes.filter(p => !p.liveness_configured).length;
    const missingReady = probes.filter(p => !p.readiness_configured).length;

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Health Probe Doctor & Anti-Patterns</h1>
          <p>Identify missing liveness/readiness probes, cascading timeouts, startup probe gaps, and deep-check failure traps.</p>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Workloads Audited</div>
          <div class="sre-kpi-val">${probes.length}</div>
          <div class="sre-kpi-meta">Containers inspected</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">High / Critical Risk</div>
          <div class="sre-kpi-val" style="color: ${criticalCount > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${criticalCount}</div>
          <div class="sre-kpi-meta">Vulnerable to outage cascading</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Missing Liveness</div>
          <div class="sre-kpi-val" style="color: ${missingLive > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${missingLive}</div>
          <div class="sre-kpi-meta">Unrecoverable deadlock risk</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Missing Readiness</div>
          <div class="sre-kpi-val" style="color: ${missingReady > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${missingReady}</div>
          <div class="sre-kpi-meta">Premature traffic routing</div>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Workload</th>
              <th>Namespace</th>
              <th>Liveness Probe</th>
              <th>Readiness Probe</th>
              <th>Startup Probe</th>
              <th>Anti-Patterns Detected</th>
              <th>Risk Level</th>
            </tr>
          </thead>
          <tbody>
            ${probes.map(p => `
              <tr>
                <td><strong>${escapeHTML(p.workload_name)}</strong></td>
                <td><code>${escapeHTML(p.namespace)}</code></td>
                <td>
                  ${p.liveness_configured ? `
                    <span class="admin-badge admin-badge-success">CONFIGURED</span>
                  ` : `
                    <span class="admin-badge admin-badge-danger">MISSING</span>
                  `}
                </td>
                <td>
                  ${p.readiness_configured ? `
                    <span class="admin-badge admin-badge-success">CONFIGURED</span>
                  ` : `
                    <span class="admin-badge admin-badge-warning">MISSING</span>
                  `}
                </td>
                <td>
                  ${p.startup_configured ? `
                    <span class="admin-badge admin-badge-success">CONFIGURED</span>
                  ` : `
                    <span class="admin-badge">NONE</span>
                  `}
                </td>
                <td>
                  ${p.anti_patterns && p.anti_patterns.length > 0 ? `
                    <div>
                      ${p.anti_patterns.map(ap => `<span class="sre-antipattern-tag">${escapeHTML(ap)}</span>`).join("")}
                    </div>
                  ` : `
                    <span style="color: var(--admin-green); font-weight: 600;">None (Clean)</span>
                  `}
                </td>
                <td>
                  <span class="admin-badge ${p.risk_level === "critical" ? "admin-badge-danger" : p.risk_level === "high" ? "admin-badge-warning" : "admin-badge-success"}">
                    ${escapeHTML(p.risk_level.toUpperCase())}
                  </span>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to audit probes: ${escapeHTML(err.message)}</div>`;
  }
}

// 4. Multi-AZ & PDB Resilience Auditor
async function renderSREResilience(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/resilience-audit?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/resilience-audit`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Multi-AZ & PDB Resilience Auditor</h1>
        <p>Detect zone concentration risks, single points of failure, and missing PodDisruptionBudgets (PDB) during node drains.</p>
      </div>
    </div>
    <div class="admin-empty-state">Auditing cluster resilience and zone spread…</div>
  `;

  try {
    const res = await request(url);
    const workloads = (res && Array.isArray(res.workloads)) ? res.workloads : (Array.isArray(res) ? res : []);
    if (!workloads || workloads.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>Multi-AZ & PDB Resilience Auditor</h1>
            <p>Detect zone concentration risks, single points of failure, and missing PodDisruptionBudgets (PDB) during node drains.</p>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Workloads Found to Audit</h3>
          </div>
        </div>
      `;
      return;
    }

    const multiAZCount = workloads.filter(r => r.multi_az_status || r.multi_az_spread).length;
    const missingPDB = workloads.filter(r => !r.pdb_configured).length;
    const singleReplica = workloads.filter(r => r.replicas <= 1).length;

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Multi-AZ & PDB Resilience Auditor</h1>
          <p>Detect zone concentration risks, single points of failure, and missing PodDisruptionBudgets (PDB) during node drains.</p>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Audited Workloads</div>
          <div class="sre-kpi-val">${workloads.length}</div>
          <div class="sre-kpi-meta">High availability checks</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Multi-AZ Distributed</div>
          <div class="sre-kpi-val" style="color: var(--admin-green);">${multiAZCount}</div>
          <div class="sre-kpi-meta">Zone-isolated failure resilience</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Missing PDBs</div>
          <div class="sre-kpi-val" style="color: ${missingPDB > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${missingPDB}</div>
          <div class="sre-kpi-meta">Vulnerable to maintenance evictions</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Single Replica SPOFs</div>
          <div class="sre-kpi-val" style="color: ${singleReplica > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${singleReplica}</div>
          <div class="sre-kpi-meta">No redundancy during downtime</div>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Workload</th>
              <th>Namespace</th>
              <th>Replicas</th>
              <th>Zones Covered</th>
              <th>Multi-AZ Spread</th>
              <th>PDB Configured</th>
              <th>Recommendations</th>
            </tr>
          </thead>
          <tbody>
            ${workloads.map(r => `
              <tr>
                <td><strong>${escapeHTML(r.workload_name)}</strong></td>
                <td><code>${escapeHTML(r.namespace)}</code></td>
                <td>${r.replicas}</td>
                <td>${escapeHTML(r.zones_covered || "Single Node/Zone")}</td>
                <td>
                  ${r.multi_az_status ? `
                    <span class="admin-badge admin-badge-success">MULTI-AZ</span>
                  ` : `
                    <span class="admin-badge admin-badge-danger">ZONE-CONCENTRATED</span>
                  `}
                </td>
                <td>
                  ${r.pdb_configured ? `
                    <span class="admin-badge admin-badge-success">${escapeHTML(r.pdb_name)}</span>
                  ` : `
                    <span class="admin-badge admin-badge-danger">MISSING PDB</span>
                  `}
                </td>
                <td style="font-size: 12px; color: var(--admin-text-muted);">
                  ${r.recommendation ? escapeHTML(r.recommendation) : "Configured according to SRE best practices."}
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to audit resilience: ${escapeHTML(err.message)}</div>`;
  }
}

// 5. Progressive Delivery & Canary Guard
async function renderSRECanaries(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/canaries?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/canaries`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Progressive Delivery & Canary Guard</h1>
        <p>Monitor live canary rollouts, traffic split ratios, error differentials, and 1-click abort fail-safes.</p>
      </div>
    </div>
    <div class="admin-empty-state">Detecting canary deployments and traffic splits…</div>
  `;

  try {
    const res = await request(url);
    const canaries = (res && Array.isArray(res.canaries)) ? res.canaries : (Array.isArray(res) ? res : []);
    if (!canaries || canaries.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>Progressive Delivery & Canary Guard</h1>
            <p>Monitor live canary rollouts, traffic split ratios, error differentials, and 1-click abort fail-safes.</p>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Active Canary Deployments Detected</h3>
            <p style="color: var(--admin-text-muted);">
              When deployments contain active canary cohorts (e.g. <code>app-canary</code> or weight annotations), they will appear here with automated anomaly detection.
            </p>
          </div>
        </div>
      `;
      return;
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Progressive Delivery & Canary Guard</h1>
          <p>Monitor live canary rollouts, traffic split ratios, error differentials, and 1-click abort fail-safes.</p>
        </div>
      </div>

      <div style="display: flex; flex-direction: column; gap: 16px;">
        ${canaries.map(c => `
          <div class="sre-signal-card">
            <div class="sre-signal-header">
              <div style="display: flex; align-items: center; gap: 10px;">
                <span class="admin-kind-badge">CANARY</span>
                <strong style="font-size: 15px;">${escapeHTML(c.name)}</strong>
                <span style="font-size: 12px; color: var(--admin-text-muted);">${escapeHTML(c.namespace)}</span>
              </div>
              <div style="display: flex; align-items: center; gap: 10px;">
                <span class="admin-badge ${c.status === "stable" ? "admin-badge-success" : c.status === "degraded" ? "admin-badge-danger" : "admin-badge-warning"}">
                  ${escapeHTML(c.status.toUpperCase())}
                </span>
                <button class="admin-btn admin-btn-danger admin-btn-sm" type="button" onclick="abortSRECanary('${escapeHTML(c.namespace)}', '${escapeHTML(c.name)}')">
                  Abort & Rollback
                </button>
              </div>
            </div>

            <div>
              <div style="display: flex; justify-content: space-between; font-size: 12px; font-weight: 600; margin-bottom: 4px;">
                <span>Primary: ${c.primary_weight_pct}% (${c.primary_replicas} pods)</span>
                <span style="color: #10b981;">Canary: ${c.canary_weight_pct}% (${c.canary_replicas} pods)</span>
              </div>
              <div class="sre-canary-bar-wrap">
                <div class="sre-canary-primary-fill" style="width: ${c.primary_weight_pct}%;">Primary ${c.primary_weight_pct}%</div>
                <div class="sre-canary-canary-fill" style="width: ${c.canary_weight_pct}%;">Canary ${c.canary_weight_pct}%</div>
              </div>
            </div>

            <div class="sre-signal-grid">
              <div class="sre-signal-metric">
                <div class="sre-signal-metric-label">Primary Error Rate</div>
                <div class="sre-signal-metric-value">${c.primary_error_rate_pct.toFixed(2)}%</div>
              </div>
              <div class="sre-signal-metric">
                <div class="sre-signal-metric-label">Canary Error Rate</div>
                <div class="sre-signal-metric-value" style="color: ${c.canary_error_rate_pct > c.primary_error_rate_pct ? "var(--admin-red)" : "var(--admin-green)"};">
                  ${c.canary_error_rate_pct.toFixed(2)}%
                </div>
              </div>
              <div class="sre-signal-metric">
                <div class="sre-signal-metric-label">Primary P99 Latency</div>
                <div class="sre-signal-metric-value">${Math.round(c.primary_p99_ms)}ms</div>
              </div>
              <div class="sre-signal-metric">
                <div class="sre-signal-metric-label">Canary P99 Latency</div>
                <div class="sre-signal-metric-value">${Math.round(c.canary_p99_ms)}ms</div>
              </div>
            </div>
          </div>
        `).join("")}
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to load canaries: ${escapeHTML(err.message)}</div>`;
  }
}

async function abortSRECanary(ns, name) {
  if (!confirm(`Are you sure you want to abort canary '${name}' in namespace '${ns}' and roll back traffic immediately?`)) return;
  try {
    await request("/api/v1/admin/sre/canaries/abort", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace: ns, name }),
    });
    alert(`Canary ${name} aborted and rolled back successfully.`);
    renderCurrentView();
  } catch (err) {
    alert("Failed to abort canary: " + err.message);
  }
}

// 6. Safe Chaos Engineering Prober
async function renderSREChaos(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Chaos Resilience Prober & Game Days</h1>
        <p>Run guarded chaos engineering experiments with mandatory 2-replica safety locks, dry-run simulations, and MTTD tracking.</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-danger admin-btn-sm" type="button" onclick="openTriggerChaosModal()">+ Trigger Safe Chaos</button>
      </div>
    </div>
    <div class="admin-empty-state">Loading chaos experiment history…</div>
  `;

  try {
    const res = await request("/api/v1/admin/sre/chaos/experiments");
    const runsRaw = (res && Array.isArray(res.recent_runs)) ? res.recent_runs : (Array.isArray(res) ? res : []);
    const runs = runsRaw.map(r => ({
      ...r,
      title: r.title || r.target_name || (r.experiment_type ? r.experiment_type.replace(/_/g, " ").toUpperCase() : "Chaos Drill"),
      target_workload: r.target_workload || r.target_name || "-",
      namespace: r.namespace || "default",
      experiment_type: r.experiment_type || "pod_kill",
      blast_radius: r.blast_radius !== undefined ? r.blast_radius : 1,
      dry_run: Boolean(r.dry_run),
      status: r.status || "completed",
    }));
    const totalRuns = runs.length;
    const completed = runs.filter(r => (r.status || "").toLowerCase() === "completed" || (r.status || "").toLowerCase() === "success").length;
    const dryRuns = runs.filter(r => r.dry_run).length;

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Chaos Resilience Prober & Game Days</h1>
          <p>Run guarded chaos engineering experiments with mandatory 2-replica safety locks, dry-run simulations, and MTTD tracking.</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-danger admin-btn-sm" type="button" onclick="openTriggerChaosModal()">+ Trigger Safe Chaos</button>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Executed Experiments</div>
          <div class="sre-kpi-val">${totalRuns}</div>
          <div class="sre-kpi-meta">Game days & resilience tests</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Successful Passes</div>
          <div class="sre-kpi-val" style="color: var(--admin-green);">${completed}</div>
          <div class="sre-kpi-meta">Recovered within SLO targets</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Dry-Run Validations</div>
          <div class="sre-kpi-val">${dryRuns}</div>
          <div class="sre-kpi-meta">Zero production impact</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Replica Safety Gate</div>
          <div class="sre-kpi-val" style="color: var(--admin-green);">ENFORCED</div>
          <div class="sre-kpi-meta">≥ 2 replicas required</div>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th>Experiment</th>
              <th>Target Workload</th>
              <th>Type</th>
              <th>Blast Radius</th>
              <th>Mode</th>
              <th>Status</th>
              <th>Recovery Time</th>
              <th>Executed By</th>
              <th>Timestamp</th>
            </tr>
          </thead>
          <tbody>
            ${runs && runs.length > 0 ? runs.map(r => `
              <tr>
                <td><strong>${escapeHTML(r.title)}</strong></td>
                <td><code>${escapeHTML(r.namespace)}/${escapeHTML(r.target_workload)}</code></td>
                <td><span class="admin-badge">${escapeHTML(r.experiment_type)}</span></td>
                <td>${r.blast_radius} pod(s)</td>
                <td>
                  ${r.dry_run ? `
                    <span class="admin-badge" style="background: #e0f2fe; color: #0369a1;">DRY-RUN</span>
                  ` : `
                    <span class="admin-badge" style="background: #fee2e2; color: #991b1b;">LIVE KILL</span>
                  `}
                </td>
                <td>
                  <span class="admin-badge ${r.status === "completed" ? "admin-badge-success" : r.status === "blocked" ? "admin-badge-danger" : "admin-badge-warning"}">
                    ${escapeHTML(r.status.toUpperCase())}
                  </span>
                </td>
                <td>${r.recovery_time_sec !== undefined ? `${r.recovery_time_sec}s` : "—"}</td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(r.executed_by || "admin")}</td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(r.created_at || "recently")}</td>
              </tr>
            `).join("") : `
              <tr>
                <td colspan="9" style="text-align: center; color: var(--admin-text-muted); padding: 24px;">
                  No chaos experiments have been recorded yet. Click "+ Trigger Safe Chaos" to run your first game day drill.
                </td>
              </tr>
            `}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to load chaos experiments: ${escapeHTML(err.message)}</div>`;
  }
}

function openTriggerChaosModal(ns = "", workload = "") {
  const modal = $("#admin-chaos-modal");
  if (!modal) return;
  modal.classList.add("open");
  if (ns) $("#chaos-namespace-input").value = ns;
  else $("#chaos-namespace-input").value = state.selectedNamespace || "default";
  if (workload) $("#chaos-workload-input").value = workload;
  $("#chaos-title-input").focus();
}

async function handleRunChaos(e) {
  e.preventDefault();
  const title = $("#chaos-title-input").value.trim();
  const ns = $("#chaos-namespace-input").value.trim();
  const workload = $("#chaos-workload-input").value.trim();
  const expType = $("#chaos-type-select").value;
  const blast = parseInt($("#chaos-blast-input").value, 10) || 1;
  const dryRun = $("#chaos-dry-run-checkbox").checked;

  const btn = $("#chaos-submit-btn");
  btn.disabled = true;
  btn.textContent = "Executing Experiment…";

  try {
    const res = await request("/api/v1/admin/sre/chaos/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        title,
        namespace: ns,
        target_workload: workload,
        experiment_type: expType,
        blast_radius: blast,
        dry_run: dryRun,
      }),
    });

    $("#admin-chaos-modal").classList.remove("open");
    $("#chaos-form").reset();
    alert(`Chaos experiment finished with status: ${res.status.toUpperCase()}.\nDetails: ${res.details || "Experiment executed within safety gates."}`);
    renderCurrentView();
  } catch (err) {
    alert("Chaos experiment failed safety constraint: " + err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = "Run Safe Experiment";
  }
}

// 7. Service Reliability & Incident Scorecards
async function renderSREScorecards(container) {
  const ns = state.selectedNamespace || "";
  const url = ns ? `/api/v1/admin/sre/reliability-scorecards?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/sre/reliability-scorecards`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Service Reliability & Incident Scorecards</h1>
        <p>A-F reliability grading, Mean Time to Detect (MTTD), Mean Time to Resolve (MTTR), and production readiness criteria.</p>
      </div>
    </div>
    <div class="admin-empty-state">Computing reliability scorecards…</div>
  `;

  try {
    const res = await request(url);
    const cardsRaw = (res && Array.isArray(res.scorecards)) ? res.scorecards : (Array.isArray(res) ? res : []);
    if (!cardsRaw || cardsRaw.length === 0) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title">
            <h1>Service Reliability & Incident Scorecards</h1>
            <p>A-F reliability grading, Mean Time to Detect (MTTD), Mean Time to Resolve (MTTR), and production readiness criteria.</p>
          </div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 20px;">
            <h3>No Workloads Found for Scorecard Evaluation</h3>
          </div>
        </div>
      `;
      return;
    }

    const cards = cardsRaw.map(c => ({
      ...c,
      service: c.service || c.service_name || "-",
      grade: c.grade || c.overall_grade || "B",
      score: Number(c.score !== undefined ? c.score : (c.overall_score !== undefined ? c.overall_score : 80)),
    }));

    const avgScore = (cards.reduce((acc, c) => acc + c.score, 0) / (cards.length || 1)).toFixed(1);
    const gradeCounts = { A: 0, B: 0, C: 0, D: 0, F: 0 };
    cards.forEach(c => {
      const g = (c.grade || "C").charAt(0).toUpperCase();
      if (gradeCounts[g] !== undefined) gradeCounts[g]++;
    });

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Service Reliability & Incident Scorecards</h1>
          <p>A-F reliability grading, Mean Time to Detect (MTTD), Mean Time to Resolve (MTTR), and production readiness criteria.</p>
        </div>
      </div>

      <div class="sre-kpi-grid">
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Average Reliability Index</div>
          <div class="sre-kpi-val">${avgScore} / 100</div>
          <div class="sre-kpi-meta">Weighted multi-factor score</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Grade A (Elite Services)</div>
          <div class="sre-kpi-val" style="color: var(--admin-green);">${gradeCounts.A}</div>
          <div class="sre-kpi-meta">Full HA & observability compliance</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Grade B / C (Standard)</div>
          <div class="sre-kpi-val" style="color: var(--admin-amber);">${gradeCounts.B + gradeCounts.C}</div>
          <div class="sre-kpi-meta">Minor resilience improvements needed</div>
        </div>
        <div class="sre-kpi-card">
          <div class="sre-kpi-label">Grade D / F (At Risk)</div>
          <div class="sre-kpi-val" style="color: ${gradeCounts.D + gradeCounts.F > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${gradeCounts.D + gradeCounts.F}</div>
          <div class="sre-kpi-meta">Action required before peak load</div>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 60px;">Grade</th>
              <th>Service</th>
              <th>Namespace</th>
              <th>Score</th>
              <th>MTTD</th>
              <th>MTTR</th>
              <th>MTBF</th>
              <th>Production Readiness Checklist</th>
            </tr>
          </thead>
          <tbody>
            ${cards.map(c => `
              <tr>
                <td>
                  <span class="sre-grade-badge sre-grade-${escapeHTML(c.grade)}">${escapeHTML(c.grade)}</span>
                </td>
                <td><strong>${escapeHTML(c.service)}</strong></td>
                <td><code>${escapeHTML(c.namespace)}</code></td>
                <td>
                  <strong>${c.score}</strong> / 100
                </td>
                <td>${c.mttd_minutes}m</td>
                <td>${c.mttr_minutes}m</td>
                <td>${c.mtbf_hours}h</td>
                <td style="font-size: 11px;">
                  <span class="admin-badge ${c.has_probes ? "admin-badge-success" : "admin-badge-danger"}">Probes</span>
                  <span class="admin-badge ${c.has_multi_az ? "admin-badge-success" : "admin-badge-warning"}">Multi-AZ</span>
                  <span class="admin-badge ${c.has_pdb ? "admin-badge-success" : "admin-badge-danger"}">PDB</span>
                  <span class="admin-badge ${c.has_limits ? "admin-badge-success" : "admin-badge-warning"}">Limits</span>
                  <span class="admin-badge ${c.has_slo ? "admin-badge-success" : "admin-badge"}">SLO</span>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="admin-empty-state">Failed to load reliability scorecards: ${escapeHTML(err.message)}</div>`;
  }
}

// -------------------------------------------------------------
// Quick Launcher Command Palette (Cmd+K / Ctrl+K)
// -------------------------------------------------------------
const COMMAND_PALETTE_ITEMS = [
  { title: "Console Docs & Component Guide", category: "Documentation", hash: "docs", pictogram: "book", desc: "Interactive architecture breakdown and component guide across all 4 operational domains + Lens vs Crelan matrix" },
  { title: "SLO & Error Budget Burn Radar", category: "SRE & Reliability", hash: "sre-slos", pictogram: "clock", desc: "Track SLO targets, error budget burn rates, freeze alerts, and historical burn velocity" },
  { title: "The 4 Golden Signals & RED Engine", category: "SRE & Reliability", hash: "sre-golden-signals", pictogram: "activity", desc: "Monitor Rate, Errors, Duration (P50/P90/P99), and Saturation across cluster services" },
  { title: "Health Probe Doctor & Anti-Patterns", category: "SRE & Reliability", hash: "sre-probes", pictogram: "activity", desc: "Detect missing liveness/readiness probes, cascading timeouts, and startup failures" },
  { title: "Multi-AZ & PDB Resilience Auditor", category: "SRE & Reliability", hash: "sre-resilience", pictogram: "shield", desc: "Audit cross-AZ zone spread topology, single points of failure, and PodDisruptionBudget gaps" },
  { title: "Progressive Delivery & Canary Guard", category: "SRE & Reliability", hash: "sre-canary", pictogram: "layers", desc: "Visualize canary vs baseline traffic splits, error differentials, and 1-click rollback" },
  { title: "Guarded Chaos Engineering Prober", category: "SRE & Reliability", hash: "sre-chaos", pictogram: "flame", desc: "Run controlled pod disruption & latency game days with strict 2-replica safety gates" },
  { title: "Service Reliability & Incident Scorecards", category: "SRE & Reliability", hash: "sre-scorecards", pictogram: "activity", desc: "A-F reliability grading, MTTD, MTTR, MTBF, and uptime stability indices" },
  { title: "Container Image CVE Radar", category: "Enterprise Suite", hash: "image-vulnerabilities", pictogram: "shield", desc: "Scan container images for CVE vulnerabilities and view package remediation" },
  { title: "Helm Releases Manager", category: "Enterprise Suite", hash: "helm-releases", pictogram: "cube", desc: "Discover Helm releases, inspect values.yaml, rollback, and uninstall" },
  { title: "Custom Resource Definitions (CRDs)", category: "Enterprise Suite", hash: "crds", pictogram: "layers", desc: "Browse installed CRDs, schemas, and live custom operator resources" },
  { title: "Interactive RBAC Tester (can-i)", category: "Enterprise Suite", hash: "can-i", pictogram: "key", desc: "Test RBAC authorization verdicts across users, groups, and service accounts" },
  { title: "Multi-Cluster Fleet Dashboard", category: "Enterprise Suite", hash: "cluster-fleet", pictogram: "layers", desc: "Monitor cluster fleet status and switch Kubernetes contexts" },
  { title: "Cluster Overview", category: "Cluster", hash: "overview", pictogram: "activity", desc: "CPU/Memory resource gauges, node/pod statistics, and cluster health" },
  { title: "Nodes & Node Health", category: "Cluster", hash: "nodes", pictogram: "server", desc: "Inspect Kubernetes nodes, taints, capacity, and conditions" },
  { title: "Pods & Containers", category: "Workloads", hash: "pods", pictogram: "cube", desc: "Explore running pods, container logs, exec terminal, and restarts" },
  { title: "Deployments", category: "Workloads", hash: "deployments", pictogram: "layers", desc: "Manage deployments, rollout restarts, and replica scaling" },
  { title: "StatefulSets", category: "Workloads", hash: "statefulsets", pictogram: "database", desc: "Stateful workloads, volume claim templates, and status" },
  { title: "DaemonSets", category: "Workloads", hash: "daemonsets", pictogram: "server", desc: "Per-node daemon workloads and scheduled pods" },
  { title: "Jobs & CronJobs", category: "Workloads", hash: "jobs", pictogram: "clock", desc: "Batch jobs and scheduled cron schedules" },
  { title: "Services & Endpoints", category: "Networking", hash: "services", pictogram: "network", desc: "ClusterIP, NodePort, and LoadBalancer routing" },
  { title: "Ingress Controllers", category: "Networking", hash: "ingresses", pictogram: "network", desc: "HTTP/HTTPS routing rules, hosts, and TLS certificates" },
  { title: "Network Policies", category: "Networking", hash: "networkpolicies", pictogram: "shield", desc: "Ingress/egress isolation rules and pod selectors" },
  { title: "ConfigMaps & Secrets", category: "Config & Storage", hash: "configmaps", pictogram: "key", desc: "Cluster configuration maps and encoded secrets" },
  { title: "PersistentVolumeClaims (PVCs)", category: "Config & Storage", hash: "pvcs", pictogram: "database", desc: "Storage claims, sizes, and bound PersistentVolumes" },
  { title: "Persistent Volumes (PVs)", category: "Config & Storage", hash: "pvs", pictogram: "database", desc: "Cluster storage volumes, capacity, and reclaim policies" },
  { title: "StorageClasses", category: "Config & Storage", hash: "storageclasses", pictogram: "database", desc: "Dynamic provisioners and volume expansion rules" },
  { title: "Namespaces & Tenant Projects", category: "Cluster", hash: "namespaces", pictogram: "cube", desc: "Namespace isolation, quotas, and customer tenant bindings" },
  { title: "Cluster Warning Events", category: "Cluster", hash: "events", pictogram: "activity", desc: "Real-time cluster warning event stream and reasons" },
  { title: "AI Diagnostic Copilot", category: "Intelligence", hash: "copilot", pictogram: "sparkles", desc: "Cluster troubleshooting and natural language remediation engine" },
  { title: "Incident War Room", category: "Intelligence", hash: "war-room", pictogram: "flame", desc: "Live incident triage, root-cause diagnosis, and post-mortem runbooks" },
  { title: "Right-Sizing & Cost Optimization", category: "Intelligence", hash: "rightsizing", pictogram: "activity", desc: "Over-provisioned CPU/Memory detection and cost savings" },
  { title: "Disaster Recovery Radar", category: "Intelligence", hash: "backup-radar", pictogram: "database", desc: "Unprotected PVC discovery, RPO tracking, and backup verification" },
  { title: "GitOps Drift Detector", category: "Intelligence", hash: "gitops-drift", pictogram: "git-branch", desc: "Detect out-of-sync cluster resources and manual kubectl overrides" },
  { title: "Network Topology Visualizer", category: "Intelligence", hash: "topology", pictogram: "network", desc: "Interactive cross-namespace service traffic and ingress graph" },
  { title: "Capsule Tenant Isolation Matrix", category: "Security", hash: "isolation-matrix", pictogram: "shield", desc: "Multi-tenant namespace boundaries and cross-tenant leak checks" },
  { title: "TLS Certificate Expiry Radar", category: "Security", hash: "certificates", pictogram: "shield", desc: "Expiring TLS certificates and cert-manager issuers" },
  { title: "Connect Kubernetes Cluster", category: "Cluster", hash: "cluster-connect", pictogram: "network", desc: "Upload kubeconfig to connect a new Kubernetes cluster" },
];

let selectedCommandPaletteIndex = 0;
let currentPaletteResults = [];

function openCommandPalette(initialQuery = "") {
  const modal = $("#admin-cmd-k-modal");
  if (!modal) return;
  modal.classList.add("open");
  const input = $("#cmd-k-input");
  input.value = initialQuery;
  input.focus();
  filterCommandPalette(initialQuery);
}

function closeCommandPalette() {
  const modal = $("#admin-cmd-k-modal");
  if (modal) modal.classList.remove("open");
}

function filterCommandPalette(q = "") {
  const query = q.toLowerCase().trim();
  if (!query) {
    currentPaletteResults = COMMAND_PALETTE_ITEMS.slice(0, 10);
  } else {
    currentPaletteResults = COMMAND_PALETTE_ITEMS.filter(item =>
      item.title.toLowerCase().includes(query) ||
      item.category.toLowerCase().includes(query) ||
      item.desc.toLowerCase().includes(query) ||
      item.hash.toLowerCase().includes(query)
    );
  }

  selectedCommandPaletteIndex = 0;
  renderCommandPaletteResults();
}

function renderCommandPaletteResults() {
  const container = $("#cmd-k-results");
  if (!container) return;

  if (currentPaletteResults.length === 0) {
    container.innerHTML = `
      <div style="padding: 24px; text-align: center; color: var(--admin-text-muted); font-size: 13px;">
        No actions or resources matched your query. Press Enter to search with AI Copilot.
      </div>
    `;
    return;
  }

  container.innerHTML = currentPaletteResults.map((item, idx) => `
    <div class="admin-cmd-k-item ${idx === selectedCommandPaletteIndex ? "selected" : ""}" data-index="${idx}" onclick="executePaletteItem(${idx})">
      <span class="admin-cmd-k-item-icon" style="color: var(--admin-accent); display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; border-radius: 6px; background: var(--admin-surface-hover);">${getSvgIcon(item.pictogram || "cube", 15)}</span>
      <div class="admin-cmd-k-item-details">
        <div class="admin-cmd-k-item-title">${escapeHTML(item.title)}</div>
        <div class="admin-cmd-k-item-desc">${escapeHTML(item.desc)}</div>
      </div>
      <span class="admin-cmd-k-item-category">${escapeHTML(item.category)}</span>
    </div>
  `).join("");
}

function executePaletteItem(idx) {
  const item = currentPaletteResults[idx];
  closeCommandPalette();
  if (!item) return;
  setRoute(item.hash);
}

function handleCommandPaletteKeydown(e) {
  if (e.key === "ArrowDown") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      selectedCommandPaletteIndex = (selectedCommandPaletteIndex + 1) % currentPaletteResults.length;
      renderCommandPaletteResults();
      scrollPaletteSelectionIntoView();
    }
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      selectedCommandPaletteIndex = (selectedCommandPaletteIndex - 1 + currentPaletteResults.length) % currentPaletteResults.length;
      renderCommandPaletteResults();
      scrollPaletteSelectionIntoView();
    }
  } else if (e.key === "Enter") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      executePaletteItem(selectedCommandPaletteIndex);
    } else {
      // Fallback: send to AI Copilot
      const val = $("#cmd-k-input")?.value || "";
      closeCommandPalette();
      if (val) {
        setRoute("copilot");
      }
    }
  } else if (e.key === "Escape") {
    e.preventDefault();
    closeCommandPalette();
  }
}


window.openCommandPalette = openCommandPalette;
window.closeCommandPalette = closeCommandPalette;
window.filterCommandPalette = filterCommandPalette;
window.executePaletteItem = executePaletteItem;

// Top-level global hotkey listener for Quick Launcher
window.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "K")) {
    e.preventDefault();
    openCommandPalette();
  }
});

function scrollPaletteSelectionIntoView() {
  const selectedEl = document.querySelector(".admin-cmd-k-item.selected");
  if (selectedEl) {
    selectedEl.scrollIntoView({ block: "nearest" });
  }
}


// -------------------------------------------------------------
// Scalable SVG Pictograms & Icon System
// -------------------------------------------------------------
function getSvgIcon(name, size = 16) {
  switch (name) {
    case "search":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>`;
    case "check":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>`;
    case "chevron-down":
      return `<svg class="admin-accordion-arrow" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>`;
    case "cube":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path><polyline points="3.27 6.96 12 12.01 20.73 6.96"></polyline><line x1="12" y1="22.08" x2="12" y2="12"></line></svg>`;
    case "activity":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"></polyline></svg>`;
    case "shield":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg>`;
    case "server":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"></rect><rect x="2" y="14" width="20" height="8" rx="2" ry="2"></rect><line x1="6" y1="6" x2="6.01" y2="6"></line><line x1="6" y1="18" x2="6.01" y2="18"></line></svg>`;
    case "database":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"></ellipse><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"></path><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"></path></svg>`;
    case "network":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="16" y="16" width="6" height="6" rx="1"></rect><rect x="2" y="16" width="6" height="6" rx="1"></rect><rect x="9" y="2" width="6" height="6" rx="1"></rect><path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"></path><path d="M12 12V8"></path></svg>`;
    case "git-branch":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="3" x2="6" y2="15"></line><circle cx="18" cy="6" r="3"></circle><circle cx="6" cy="18" r="3"></circle><path d="M18 9a9 9 0 0 1-9 9"></path></svg>`;
    case "sparkles":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.912 5.885a2 2 0 0 0 1.203 1.203L21 12l-5.885 1.912a2 2 0 0 0-1.203 1.203L12 21l-1.912-5.885a2 2 0 0 0-1.203-1.203L3 12l5.885-1.912a2 2 0 0 0 1.203-1.203L12 3z"></path></svg>`;
    case "flame":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8.5 14.5A2.5 2.5 0 0 0 11 12c0-1.38-.5-2-1-3-1.072-2.143-.224-4.054 2-6 .5 2.5 2 4.9 4 6.5 2 1.6 3 3.5 3 5.5a7 7 0 1 1-14 0c0-1.153.433-2.294 1-3a2.5 2.5 0 0 0 2.5 2.5z"></path></svg>`;
    case "clock":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>`;
    case "key":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"></path></svg>`;
    case "layers":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"></polygon><polyline points="2 17 12 22 22 17"></polyline><polyline points="2 12 12 17 22 12"></polyline></svg>`;
    case "book":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"></path><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"></path></svg>`;
    case "terminal":
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"></polyline><line x1="12" y1="19" x2="20" y2="19"></line></svg>`;
    default:
      return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle></svg>`;
  }
}

function toggleAllDocsAccordions() {
  const accordions = document.querySelectorAll(".admin-docs-accordion");
  if (!accordions.length) return;
  const anyClosed = Array.from(accordions).some(a => !a.open);
  accordions.forEach(a => a.open = anyClosed);
  const btn = document.getElementById("docs-toggle-accordions-btn");
  if (btn) btn.textContent = anyClosed ? "Collapse All" : "Expand All";
}

// -------------------------------------------------------------
// Admin Documentation & Component Architecture Guide
// -------------------------------------------------------------

const ADMIN_COMPONENTS = [
  // ==================== DOMAIN 1: CLUSTER CORE ====================
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Cluster Overview & Telemetry",
    pictogram: "activity", route: "overview",
    badge: "Core",
    desc: "Real-time cluster telemetry dashboard providing CPU, Memory, and Pod capacity utilization gauges alongside workload counts, warning event counters, and node health status.",
    features: [
      "Real-time CPU and Memory allocation vs cluster capacity gauges",
      "Live counter cards for Nodes, Pods, Deployments, and Warnings",
      "Dynamic node condition breakdown (Ready, MemoryPressure, DiskPressure)",
      "Auto-refresh interval support (5s, 10s, 30s) or manual sync"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Nodes & Node Health Inspector",
    pictogram: "server", route: "nodes",
    badge: "Infra",
    desc: "Deep-dive node management inspecting node ready states, kubelet versions, internal/external IPs, OS images, kernel versions, and node conditions.",
    features: [
      "Node condition audits: Ready, PIDPressure, DiskPressure, NetworkUnavailable",
      "Taints & tolerations visualizer to understand scheduling boundaries",
      "Allocatable vs capacity resource headroom breakdown",
      "Node cordoning & scheduling status"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Pods & Container Diagnostics",
    pictogram: "cube", route: "pods",
    badge: "Core",
    desc: "Workload container orchestrator view with live status filters (Running, Pending, Failed, CrashLoopBackOff), pod restart tracking, terminal exec, and log streaming.",
    features: [
      "Live status indicator: Running, CrashLoopBackOff, Error, OOMKilled",
      "Integrated Container Log Streamer with tail count and auto-follow",
      "Web-based Ephemeral Debug Shell & terminal exec without local kubectl",
      "Pod restart count tracking and IP / Node placement inspection"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Deployments & Workload Rollouts",
    pictogram: "layers", route: "deployments",
    badge: "Core",
    desc: "Deployment lifecycle controller enabling instant replica scaling, zero-downtime rolling restart triggers, container image inspections, and YAML editing.",
    features: [
      "1-Click Zero-Downtime Rollout Restart (`kubectl rollout restart`)",
      "Direct replica scaling up/down with instant reconciliation",
      "Rolling update strategy inspection (maxSurge, maxUnavailable)",
      "Live YAML editor and manifest inspector"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "StatefulSets",
    pictogram: "database", route: "statefulsets",
    badge: "Storage",
    desc: "Manages stateful applications requiring persistent identifiers, ordered deployment and scaling, and stable network identities.",
    features: [
      "VolumeClaimTemplate inspection and storage class association",
      "Ordered pod ordinal scaling tracking (0, 1, 2...)",
      "Service name and headless DNS binding inspector",
      "Full live YAML view and replica management"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "DaemonSets",
    pictogram: "server", route: "daemonsets",
    badge: "Infra",
    desc: "Tracks node-level daemon workloads such as logging agents, CNI networking plugins, and monitoring probers scheduled across all eligible nodes.",
    features: [
      "Desired vs Current vs Ready node schedule counts",
      "Node selector and toleration configuration inspection",
      "Update strategy tracking (RollingUpdate vs OnDelete)",
      "Direct pod list jump from DaemonSet card"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Jobs & CronJobs",
    pictogram: "clock", route: "jobs",
    badge: "Batch",
    desc: "Batch execution prober monitoring one-off batch Jobs and recurring CronJobs with schedule syntax, completion status, and last run timestamps.",
    features: [
      "Cron schedule expression parser and next run prediction",
      "Job completion, active count, and failed execution breakdown",
      "Backoff limit and active deadline inspection",
      "Manual 1-click Job trigger from CronJob template"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Services & Networking Endpoints",
    pictogram: "network", route: "services",
    badge: "Network",
    desc: "Service routing layer displaying ClusterIP, NodePort, LoadBalancer, and ExternalName services, port mappings, and backing endpoint slices.",
    features: [
      "Port mapping inspector (Port -> TargetPort / Protocol)",
      "Selector-to-pod matching validation and healthy endpoint counts",
      "External IP and LoadBalancer ingress status tracking",
      "Headless service detection and DNS verification"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Ingress Controllers & HTTP Routes",
    pictogram: "network", route: "ingresses",
    badge: "Network",
    desc: "L7 HTTP/HTTPS reverse proxy inspector monitoring Ingress rules, TLS secret bindings, host routing, and ingress controller class assignments.",
    features: [
      "Host and path routing table with target service linkages",
      "TLS secret binding and certificate issuer validation",
      "IngressClass provider inspection (nginx, traefik, alb)",
      "SSL termination and HTTPS redirect rule auditor"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Network Policies & Isolation Rules",
    pictogram: "shield", route: "networkpolicies",
    badge: "Security",
    desc: "Zero-trust firewall policies defining Ingress and Egress traffic filtering rules between pods, namespaces, and external CIDR blocks.",
    features: [
      "PodSelector visualizer to identify protected workloads",
      "Ingress and Egress rule breakdown with allowed ports and CIDRs",
      "Namespace isolation gap detection (detecting unprotected pods)",
      "Live YAML inspection and network rule verification"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "ConfigMaps & Secrets",
    pictogram: "key", route: "configmaps",
    badge: "Config",
    desc: "Environment configuration and credential vault managing application configs, TLS keys, tokens, and environment variables.",
    features: [
      "ConfigMap data key-value browser with multi-line text preview",
      "Kubernetes Secret decoder with secure show/hide toggle",
      "Secret type detection (Opaque, kubernetes.io/tls, service-account)",
      "Workload dependency audit showing pods mounting each secret"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "PersistentVolumeClaims (PVCs)",
    pictogram: "database", route: "pvcs",
    badge: "Storage",
    desc: "Storage claim manager auditing requested volume sizes, access modes (RWO/RWX), binding status, and attached StorageClasses.",
    features: [
      "Binding status audit (Bound, Pending, Lost) with error reasons",
      "Access mode inspection: ReadWriteOnce, ReadWriteMany, ReadOnlyMany",
      "StorageClass assignment and volume capacity verification",
      "Direct link to bound PersistentVolume (PV)"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Persistent Volumes (PVs)",
    pictogram: "database", route: "pvs",
    badge: "Storage",
    desc: "Cluster storage provisioner managing underlying physical storage volumes, reclaim policies (Retain, Delete), and CSI driver bindings.",
    features: [
      "Reclaim policy audit to prevent unexpected data loss upon claim deletion",
      "CSI driver provisioner identification (EBS, GPD, local, NFS)",
      "Volume mode (Filesystem vs Block) and capacity breakdown",
      "Direct linkage to bound namespace PVC"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "StorageClasses",
    pictogram: "database", route: "storageclasses",
    badge: "Storage",
    desc: "Defines dynamic storage provisioner rules, allowVolumeExpansion flags, reclaim policies, and volume binding modes (Immediate vs WaitForFirstConsumer).",
    features: [
      "Provisioner plugin discovery (e.g., ebs.csi.aws.com, pd.csi.storage.gke.io)",
      "Volume expansion capability auditor",
      "Volume binding mode inspection to troubleshoot multi-AZ scheduling",
      "Default StorageClass indicator"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Namespaces & Tenant Projects",
    pictogram: "cube", route: "namespaces",
    badge: "Multi-Tenant",
    desc: "Logical cluster partitions providing workload isolation, ResourceQuota enforcement, and customer project tenancy mapping.",
    features: [
      "Namespace lifecycle tracking (Active vs Terminating)",
      "ResourceQuota and LimitRange capacity limits vs current consumption",
      "Customer tenant workspace association",
      "Quick namespace switcher for console-wide filtering"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Cluster Events & Warning Stream",
    pictogram: "activity", route: "events",
    badge: "Telemetry",
    desc: "Streaming cluster event bus capturing Warnings, Errors, BackOffs, and Normal scheduling events emitted by Kubernetes controllers.",
    features: [
      "Warning vs Normal event severity filtering",
      "Reason aggregator: FailedScheduling, BackOff, Unhealthy, FailedMount",
      "Target resource cross-link to investigate pod or node directly",
      "Event timestamping and occurrence count tracking"
    ]
  },
  {
    domain: "k8s",
    domainLabel: "Cluster Core",
    title: "Dangling Config Scanner",
    route: "dangling-configs",
    badge: "Hygiene",
    desc: "Garbage collection and hygiene advisor scanning for orphaned ConfigMaps and Secrets not referenced by any active Deployment, Pod, or CronJob.",
    features: [
      "Cross-references all pods and workload env/volume mounts",
      "Identifies stale configuration files consuming etcd memory",
      "Highlights high-risk orphaned secrets containing sensitive credentials",
      "1-Click cleanup recommendation"
    ]
  },

  // ==================== DOMAIN 2: SRE & RELIABILITY ====================
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "SLO & Error Budget Burn Radar",
    pictogram: "clock", route: "sre-slos",
    badge: "Crelan Exclusive",
    desc: "Google SRE-standard Service Level Objective radar tracking 99.9% / 99.5% availability targets, multi-window error budget burn rates (1h, 6h, 24h, 30d), and automated deployment freeze gates.",
    features: [
      "Multi-window burn rate alert thresholds (14.4x 1h, 6x 6h) based on Google SRE Workbook",
      "Automated Deployment Freeze Gate alerting when budget is exhausted (<0%)",
      "Historical burn velocity trend tracking and remaining budget projections",
      "Direct integration with Incident War Room for rapid escalation"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "The 4 Golden Signals & RED Engine",
    pictogram: "activity", route: "sre-golden-signals",
    badge: "Crelan Exclusive",
    desc: "Real-time service telemetry engine monitoring Rate (req/sec), Errors (5xx rate), Duration (P50, P90, P99 latency percentiles), and Saturation across all cluster microservices.",
    features: [
      "P50, P90, and P99 tail latency percentiles with spike anomaly indicators",
      "5xx HTTP error rate tracking with service degradation triggers",
      "Throughput (RPS) velocity metrics per service and namespace",
      "Memory & CPU saturation thresholds alerting before OOMKilled events occur"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Health Probe Doctor & Anti-Patterns",
    pictogram: "activity", route: "sre-probes",
    badge: "Crelan Exclusive",
    desc: "Automated reliability auditor scanning every container for missing Liveness, Readiness, or Startup probes, misaligned timeouts, and cascading failure anti-patterns.",
    features: [
      "Missing Probe Scanner detecting containers vulnerable to silent deadlocks",
      "Cascading Timeout Detector: Flags probe timeouts exceeding downstream SLA limits",
      "Flapping & Probe Crash Counter tracking intermittent health check failures",
      "1-Click recommended YAML probe configuration snippet generator"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Multi-AZ & PDB Resilience Auditor",
    pictogram: "shield", route: "sre-resilience",
    badge: "Crelan Exclusive",
    desc: "High-availability auditor inspecting cross-availability-zone topology spreads (topologySpreadConstraints), Single Points of Failure (SPOF), and missing PodDisruptionBudgets (PDB).",
    features: [
      "Availability Zone (AZ) skew analysis identifying workloads co-located on a single AZ",
      "Single Point of Failure (SPOF) detector: Identifies single-replica production workloads",
      "PodDisruptionBudget (PDB) coverage audit ensuring safe node drain and cluster upgrades",
      "Cluster disruption risk score with actionable resilience hardening advice"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Progressive Delivery & Canary Guard",
    pictogram: "layers", route: "sre-canary",
    badge: "Crelan Exclusive",
    desc: "Canary rollout controller providing real-time traffic splitting visualizers (e.g. 10% canary / 90% baseline), error differential comparison, and a 1-click panic rollback button.",
    features: [
      "Live Canary vs Baseline traffic split visualizer (Argo / Flagger / Native)",
      "Error differential radar: Flags when canary 5xx error rate exceeds baseline by >1%",
      "Latency regression monitor comparing P99 between current and candidate version",
      "1-Click Immediate Abort & Rollback button restoring 100% stable baseline traffic"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Guarded Chaos Engineering Prober",
    pictogram: "flame", route: "sre-chaos",
    badge: "Crelan Exclusive",
    desc: "Safety-gated chaos engineering platform allowing engineers to inject pod disruptions, network latency, and memory spikes with an uncompromised 2-replica safety guardrail.",
    features: [
      "Strict Production Safety Gate: Refuses to run chaos on any workload with fewer than 2 healthy replicas",
      "Pod termination chaos probers to test automatic failover and recovery",
      "Controlled network latency injection to simulate cross-region / database lag",
      "Automated health verification and instant chaos abort failsafe"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Service Reliability & Incident Scorecards",
    pictogram: "activity", route: "sre-scorecards",
    badge: "Crelan Exclusive",
    desc: "Objective reliability report card grading every service from A+ to F based on probe coverage, multi-AZ deployment, PDB protection, restart counts, and MTTD/MTTR metrics.",
    features: [
      "Letter grade score (A, B, C, D, F) computed across 8 reliability dimensions",
      "Mean Time to Detect (MTTD) and Mean Time to Resolve (MTTR) tracking",
      "Mean Time Between Failures (MTBF) and 30-day uptime stability index",
      "Actionable checklist to promote any failing service to Grade A readiness"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Incident War Room & Post-Mortem Generator",
    pictogram: "flame", route: "war-room",
    badge: "Crelan Exclusive",
    desc: "Live incident response triage room synthesizing real-time error logs, affected microservices, and AI root cause diagnosis with an automated 1-click Markdown Post-Mortem Generator.",
    features: [
      "Real-time Incident War Room correlating alerts, logs, and pod events",
      "Automated Post-Mortem Generator: Builds complete Markdown post-mortem with timeline, root cause, impact, and action items in 1 click",
      "Blast radius calculator identifying affected upstream and downstream dependencies",
      "Incident status coordinator (Investigating, Identified, Monitoring, Resolved)"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Event Storm Radar & Anomaly Detector",
    route: "event-storm",
    badge: "Intelligence",
    desc: "High-frequency event velocity analyzer detecting warning spikes, cascading CrashLoopBackOffs, and kubelet thrashing before it leads to node failure.",
    features: [
      "Real-time events-per-second velocity gauge with anomaly spike detection",
      "Top offending namespaces, pods, and controllers causing event storms",
      "Pattern matching for FailedMount, Evicted, and FailedScheduling storms",
      "Throttling suggestions to prevent etcd saturation during storms"
    ]
  },
  {
    domain: "sre",
    domainLabel: "SRE & Reliability",
    title: "Autoscaling & HPA Controller",
    route: "autoscaling",
    badge: "SRE",
    desc: "Horizontal Pod Autoscaler (HPA) and Vertical Pod Autoscaler (VPA) inspector monitoring target metrics, min/max replica boundaries, and scaling event history.",
    features: [
      "Current vs Target CPU/Memory utilization percentages",
      "Min and Max replica boundaries and current scale replica counts",
      "Scaling condition auditor (AbleToScale, ScalingActive, ScalingLimited)",
      "Thrashing / flapping detection warning of oscillating scale events"
    ]
  },

  // ==================== DOMAIN 3: SECURITY & COMPLIANCE ====================
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "CIS Benchmark & Cluster Hardening Audit",
    route: "security",
    badge: "Security",
    desc: "Automated CIS Kubernetes Benchmark and NSA/CISA security auditor evaluating control-plane flags, privileged containers, root execution, and host networking.",
    features: [
      "Privileged container and `hostPID` / `hostNetwork` exploit scanner",
      "Root user execution detector (`runAsNonRoot: true` verification)",
      "Read-only root filesystem compliance auditor",
      "Security posture score with step-by-step remediation commands"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "Container Image CVE Vulnerability Radar",
    pictogram: "shield", route: "image-vulnerabilities",
    badge: "Enterprise",
    desc: "Continuous image vulnerability scanner auditing all running images against known CVE databases with CVSS scoring, affected packages, and fixed version guidance.",
    features: [
      "Real-time mapping of running image digests to CVE advisories",
      "CVSS severity categorization (Critical, High, Medium, Low)",
      "Vulnerable OS and application package identification (e.g. openssl, glibc)",
      "Patch guidance showing exact upgraded container image tag"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "RBAC Blast Radius & Privilege Auditor",
    route: "rbac",
    badge: "Security",
    desc: "Deep RBAC visualizer auditing ClusterRoles, Roles, RoleBindings, and ServiceAccounts to identify wildcard permissions (`*`), cluster-admin sprawl, and escalation risks.",
    features: [
      "Over-privileged ServiceAccount detector (e.g. `cluster-admin` bindings)",
      "Wildcard permission finder (`verbs: [*]`, `resources: [*]`)",
      "ServiceAccount token automounting risk auditor",
      "Interactive visual permission matrix across all namespaces"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "Interactive RBAC Tester (can-i)",
    pictogram: "key", route: "can-i",
    badge: "Enterprise",
    desc: "Interactive permission evaluator allowing administrators to query whether a specific user, group, or service account is authorized to perform any Kubernetes API verb on any resource.",
    features: [
      "Evaluates API permissions for Users, Groups, and ServiceAccounts",
      "Supports all verbs: get, list, watch, create, update, patch, delete, deletecollection",
      "Namespace-scoped and cluster-wide permission simulation",
      "Live explanation of matching RoleBindings granting or denying the action"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "Capsule Multi-Tenant Isolation Matrix",
    pictogram: "shield", route: "isolation-matrix",
    badge: "Enterprise",
    desc: "Enterprise multi-tenancy matrix built for Capsule and native Kubernetes, tracking tenant boundaries, project quotas, network isolation, and cross-tenant leakage.",
    features: [
      "Tenant boundary auditor preventing cross-tenant pod communication",
      "Tenant project resource quota enforcement (CPU, Memory, Ingress domains)",
      "Tenant owner and team role mapping",
      "Cross-tenant network policy verification"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "Cluster & Resource Access Control",
    route: "access-control",
    badge: "Enterprise",
    desc: "User access management system regulating administrator permissions, platform role assignments (Admin, Operator, Viewer), and API access tokens.",
    features: [
      "Role-Based Access Control (RBAC) governance for console users",
      "API token lifecycle manager with scope enforcement and instant revocation",
      "Audit logs of console privilege assignments and user changes",
      "Session management and active administrator tracking"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "TLS Certificate Expiration Radar",
    pictogram: "shield", route: "certificates",
    badge: "Security",
    desc: "Proactive TLS certificate auditor monitoring all Ingress and Secret TLS certificates, cert-manager Issuers, and ClusterIssuers before expiration causes outages.",
    features: [
      "Proactive expiration countdown (<30d warning, <7d critical)",
      "cert-manager Certificate and Issuer health and renewal status",
      "Common Name (CN), Subject Alternative Names (SANs), and CA issuer details",
      "Automated renewal trigger recommendations"
    ]
  },
  {
    domain: "security",
    domainLabel: "Security & Compliance",
    title: "Platform Audit Trail & Change Ledger",
    route: "audit",
    badge: "Security",
    desc: "Immutable change ledger recording all administrative actions, deployments, rollbacks, config updates, and terminal executions across the cluster.",
    features: [
      "Comprehensive actor tracking (User, ServiceAccount, IP address, Timestamp)",
      "Resource modification diffs for deployments, configs, and access rules",
      "Exportable audit logs for SOC2, ISO27001, and HIPAA compliance",
      "Filter by actor, action type, target namespace, or date range"
    ]
  },

  // ==================== DOMAIN 4: PLATFORM OPS & FLEET ====================
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Multi-Cluster Switcher & Fleet Dashboard",
    pictogram: "layers", route: "cluster-fleet",
    badge: "Fleet",
    desc: "ClickUp-style multi-cluster fleet management allowing platform teams to switch between multiple production, staging, and edge Kubernetes clusters instantly.",
    features: [
      "1-Click context switching across all connected Kubernetes clusters",
      "ClickUp-style customizable cluster color palettes and avatars",
      "Fleet-wide health telemetry: Node counts, pod totals, and K8s versions",
      "Add new cluster via raw kubeconfig or environment variable synchronization"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Helm Releases Manager",
    pictogram: "cube", route: "helm-releases",
    badge: "Enterprise",
    desc: "Complete Helm v3 release lifecycle manager discovering installed charts, inspecting computed values.yaml, executing 1-click rollbacks, and uninstalling releases.",
    features: [
      "Cross-namespace Helm release discovery with status and chart versions",
      "Values Inspector with syntax highlighting and 1-click clipboard copy",
      "1-Click Rollback to any previous revision with zero downtime",
      "Safe uninstall with automatic resource cleanup"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "CRD & Operator Browser",
    pictogram: "layers", route: "crds",
    badge: "Enterprise",
    desc: "Explorer for all installed Custom Resource Definitions (CRDs) and operator instances under apiextensions.k8s.io/v1.",
    features: [
      "Browse CRD groups, versions, scopes (Namespaced vs Cluster), and schemas",
      "Live operator custom resource viewer (Cert-Manager, Capsule, Istio, etc.)",
      "YAML and JSON manifest inspector for custom resources",
      "Operator condition status and schema definition validation"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "AI K8s Diagnostic Copilot",
    pictogram: "sparkles", route: "copilot",
    badge: "AI Native",
    desc: "Integrated Google Gemini-powered diagnostic assistant providing intelligent root-cause analysis, natural-language cluster querying, and instant remediation commands.",
    features: [
      "Natural-language cluster queries ('Why is auth-service crash looping?')",
      "Context-aware analysis combining live pod logs, events, and manifests",
      "Generates copy-paste kubectl and YAML remediation patches",
      "Zero telemetry leakage with strict security boundaries"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Cost & Right-Sizing Advisor",
    pictogram: "activity", route: "rightsizing",
    badge: "FinOps",
    desc: "FinOps cost intelligence engine identifying over-provisioned CPU and memory requests, calculating monthly dollar waste, and recommending optimized container limits.",
    features: [
      "Over-provisioning detector comparing actual usage against requested CPU/Memory",
      "Estimated monthly dollar waste calculation per workload and namespace",
      "Actionable right-sizing recommendations (spec.containers[*].resources)",
      "Under-provisioned workload warnings to prevent OOMKilled crashes"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Resource Efficiency & Heatmap",
    route: "efficiency",
    badge: "FinOps",
    desc: "Visual heatmaps of cluster node capacity and workload utilization, highlighting resource bottlenecks, stranded capacity, and allocation imbalances.",
    features: [
      "Interactive node CPU and memory allocation heatmap",
      "Identifies stranded memory or CPU on under-utilized worker nodes",
      "Namespace resource distribution breakdown",
      "Bin-packing efficiency score across the cluster fleet"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Database Topology & Connection Visualizer",
    route: "databases",
    badge: "Data",
    desc: "Discovers and maps managed and in-cluster database instances (PostgreSQL, MySQL, Redis, MongoDB), active connection pools, PVC bindings, and backup status.",
    features: [
      "Database workload mapping across statefulsets and managed instances",
      "Storage binding verification and PersistentVolume claim health",
      "High-availability replica topology and leader/follower status",
      "Backup protection verification and storage retention check"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Storage Leak Detector",
    route: "storage-leaks",
    badge: "Hygiene",
    desc: "FinOps storage hygiene engine scanning for unmounted PersistentVolumes, orphan cloud disks, and released PVCs silently incurring infrastructure costs.",
    features: [
      "Identifies unmounted and unattached PVs incurring cloud disk costs",
      "Calculates estimated monthly cost savings from purging storage leaks",
      "Safeguard check ensuring no volume in use is marked for purge",
      "1-Click safe storage purge trigger"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Traffic & Ingress Network Topology",
    pictogram: "network", route: "topology",
    badge: "Network",
    desc: "Interactive visual topological graph mapping external HTTP traffic from Ingresses through Services down to backing Pods across all namespaces.",
    features: [
      "Interactive topological dependency graph",
      "Visualizes Ingress -> Service -> Pod routing pathways",
      "Identifies orphaned services without matching pod selectors",
      "Highlights degraded or failing endpoints directly on the topology graph"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Cluster Upgrade Radar & Deprecations",
    route: "upgrade-radar",
    badge: "Ops",
    desc: "Pre-upgrade compatibility radar scanning cluster resources for deprecated or removed Kubernetes API versions prior to upgrading control plane or worker nodes.",
    features: [
      "Identifies deprecated API versions (e.g. autoscaling/v2beta1, networking.k8s.io/v1beta1)",
      "Target Kubernetes version compatibility scorecard (1.28, 1.29, 1.30, 1.31)",
      "Actionable replacement API group and manifest migration instructions",
      "Prevents control-plane upgrade failures caused by unsupported APIs"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "GitOps Drift Radar",
    pictogram: "git-branch", route: "gitops-drift",
    badge: "GitOps",
    desc: "Detects unauthorized out-of-band changes, manual kubectl edit modifications, and configuration drift between cluster state and GitOps repository source of truth.",
    features: [
      "Real-time drift detection comparing live state against Git commit manifests",
      "Side-by-side YAML diff viewer highlighting exact drifted fields",
      "1-Click Reconcile trigger to restore cluster state from Git",
      "Drift alert webhooks notifying Slack/PagerDuty of manual overrides"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Disaster Recovery & Backup Radar",
    pictogram: "database", route: "backup-radar",
    badge: "Resilience",
    desc: "Disaster recovery prober identifying unprotected PersistentVolumeClaims, calculating Recovery Point Objectives (RPO), and verifying backup schedules.",
    features: [
      "Unprotected PVC discovery: Flags volumes lacking snapshot or backup coverage",
      "RPO compliance auditor measuring time elapsed since last verified backup",
      "Velero / CSI volume snapshot backup status verification",
      "Disaster recovery readiness index with RTO/RPO SLA tracking"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Cluster Connect & Onboarding",
    pictogram: "network", route: "cluster-connect",
    badge: "Admin",
    desc: "Seamless onboarding wizard to connect any Kubernetes cluster via kubeconfig YAML, base64 environment variable, or in-cluster service account.",
    features: [
      "Direct kubeconfig paste or file upload with instant connection test",
      "Persistent cloud hosting instructions using CRELAN_KUBECONFIG_DATA_B64",
      "Context auto-discovery and cluster API reachability verification",
      "Multi-cluster context switcher integration"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Cluster Readiness Doctor",
    route: "readiness",
    badge: "Ops",
    desc: "Pre-flight health check engine validating API server responsiveness, CoreDNS resolution, CNI network readiness, storage provisioner status, and ingress controllers.",
    features: [
      "Control-plane API server latency and healthz check",
      "CoreDNS internal name resolution test across pods",
      "Default StorageClass dynamic provisioning readiness test",
      "CNI pod-to-pod network connectivity validator"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Control-Plane Backup",
    route: "backup",
    badge: "Ops",
    desc: "Snapshot engine generating full control plane state archives including CRDs, namespaces, role bindings, and workload specifications for point-in-time disaster recovery.",
    features: [
      "Point-in-time cluster manifest state export",
      "Automated archive generation with checksum verification",
      "Easy migration and staging environment bootstrapping",
      "Secure encrypted storage integration"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Incident Alert Webhooks",
    route: "webhooks",
    badge: "Ops",
    desc: "Configures webhook dispatchers to send real-time alerts for CrashLoopBackOffs, SLO burn rate breaches, CVE criticals, and GitOps drift to Slack, Discord, and PagerDuty.",
    features: [
      "Custom webhook endpoint manager with secret HMAC signing",
      "Granular event subscriptions (SLO freeze, Warning storms, Chaos abort, Drift)",
      "Test payload dispatcher with live delivery log inspection",
      "Native integrations with Slack, Discord, PagerDuty, and custom HTTP endpoints"
    ]
  },
  {
    domain: "ops",
    domainLabel: "Platform Ops & Fleet",
    title: "Quick Launcher Command Palette",
    route: "#cmd-k",
    badge: "Productivity",
    desc: "Keyboard-driven command palette (⌘K / Ctrl+K) offering instant navigation to any cluster resource, diagnostic engine, or administrative action in under 2 keystrokes.",
    features: [
      "Global shortcut (⌘K on macOS, Ctrl+K on Windows/Linux)",
      "Instant fuzzy search across all 52 tools and cluster resources",
      "Zero-latency jump to pods, nodes, CVE scanner, War Room, or Helm releases",
      "Accessible from anywhere in the console"
    ]
  }
];

const LENS_VS_CRELAN_MATRIX = [
  {
    category: "Architecture & Deployment",
    feature: "Deployment Model",
    lens: "Heavy Electron desktop app installed locally per operator; requires desktop software licenses.",
    crelan: "100% Web-Native, zero local client install, team-accessible via RBAC.",
    impact: "No desktop footprint, instant onboarding for whole team, accessible from browser or tablet."
  },
  {
    category: "SRE & Reliability",
    feature: "Live Post-Mortem Generator",
    lens: "None (manual document authoring).",
    crelan: "Built-in 1-Click Markdown Post-Mortem Generator with timeline & root cause.",
    impact: "Saves hours during P1 incidents; generates audit-ready post-mortems in seconds."
  },
  {
    category: "SRE & Reliability",
    feature: "SLO & Error Budget Burn Radar",
    lens: "None (requires separate external Grafana / Datadog setup).",
    crelan: "Built-in Google SRE multi-window burn rates (1h, 6h, 24h, 30d) + freeze alerts.",
    impact: "Automated deployment freeze gates when error budgets are exhausted."
  },
  {
    category: "SRE & Reliability",
    feature: "Guarded Chaos Engineering",
    lens: "None (requires external Chaos Mesh / Litmus installation).",
    crelan: "Built-in Chaos Prober with strict 2-replica production safety gates.",
    impact: "Safe production game days without risking single-replica workload downtime."
  },
  {
    category: "SRE & Reliability",
    feature: "Health Probe Doctor & Anti-Patterns",
    lens: "Raw YAML display only.",
    crelan: "Automated probe scanner detecting missing liveness/readiness probes & timeout bugs.",
    impact: "Prevents silent pod deadlocks and cascading timeouts."
  },
  {
    category: "SRE & Reliability",
    feature: "Progressive Delivery & Canary Guard",
    lens: "Basic pod lists.",
    crelan: "Live canary traffic visualizer, error differential monitor & 1-click abort rollback.",
    impact: "Safe progressive rollouts with instant rollback capability."
  },
  {
    category: "Security & Compliance",
    feature: "Container Image CVE Vulnerability Radar",
    lens: "Requires paid Lens Security Extension subscription.",
    crelan: "Built-in zero-config image CVE vulnerability scanner with CVSS scoring.",
    impact: "Instant visibility into vulnerable images and remediation tags without paid addons."
  },
  {
    category: "Security & Compliance",
    feature: "Interactive RBAC Tester (can-i)",
    lens: "Limited / manual command line only.",
    crelan: "Visual permission evaluator across Users, Groups, and ServiceAccounts.",
    impact: "Instantly verify who has access to sensitive resources without CLI access."
  },
  {
    category: "Security & Compliance",
    feature: "Capsule Multi-Tenant Isolation Matrix",
    lens: "Flat namespace listing only.",
    crelan: "Tenant project boundary auditor, quota enforcement, and cross-tenant leak checks.",
    impact: "True multi-tenancy governance for enterprise platforms."
  },
  {
    category: "Security & Compliance",
    feature: "Platform Audit Trail & Change Ledger",
    lens: "None (session actions not recorded).",
    crelan: "Immutable change ledger with user attribution, timestamps, and diff tracking.",
    impact: "SOC2, ISO27001, and HIPAA compliance readiness."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "Multi-Cluster Switching Experience",
    lens: "Catalog grid requiring desktop focus.",
    crelan: "ClickUp-style persistent rail with customizable cluster avatars & color palettes.",
    impact: "Immediate visual cluster context recognition; avoids accidental production edits."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "Helm Release Lifecycle & Values Diff",
    lens: "Basic Helm view (limited values editing).",
    crelan: "Full Helm v3 manager with values inspector, 1-click revision rollback & uninstall.",
    impact: "Zero-CLI Helm management directly in the console."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "GitOps Drift Radar",
    lens: "Requires ArgoCD desktop plugin.",
    crelan: "Built-in real-time drift detection comparing live state vs Git with side-by-side diff.",
    impact: "Catch unauthorized out-of-band kubectl changes immediately."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "Disaster Recovery & RPO Radar",
    lens: "None.",
    crelan: "Continuous unprotected PVC discovery, RPO tracking & backup verification.",
    impact: "Eliminates surprise data loss from unbacked persistent volumes."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "AI Diagnostic Copilot",
    lens: "Proprietary paid cloud add-on.",
    crelan: "Built-in Google Gemini reasoning analyzing live cluster events, logs, and YAML.",
    impact: "Plain-language troubleshooting and copy-paste remediation commands."
  },
  {
    category: "Platform Ops & Fleet",
    feature: "FinOps Cost & Storage Leak Detector",
    lens: "Separate commercial cloud extension.",
    crelan: "Built-in right-sizing recommendations + unmounted volume leak detector.",
    impact: "Immediate reduction in wasted cloud compute and orphan disk costs."
  }
];

let docsActiveTab = "all";
let docsSearchQuery = "";

async function renderDocs(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Console Documentation & Component Guide</h1>
        <p>Interactive architecture handbook detailing every tool, diagnostic engine, and SRE prober built into Crelan.</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" id="docs-open-matrix-btn">
          Lens vs Crelan Matrix
        </button>
        <button class="admin-btn admin-btn-sm admin-btn-primary" type="button" onclick="openCommandPalette()">
          <span class="admin-cmd-k-badge" style="font-size:10px; padding:1px 4px;">⌘K</span> Quick Launcher
        </button>
      </div>
    </div>

    <!-- Hero Card -->
    <div class="admin-docs-hero">
      <div class="admin-docs-hero-content">
        <div style="display:inline-flex; align-items:center; gap:6px; background:#eff6ff; color:#2563eb; font-size:11px; font-weight:700; padding:3px 10px; border-radius:20px; margin-bottom:8px; border:1px solid #bfdbfe;">
          <span>ARCHITECTURE &amp; SRE MANUAL</span>
        </div>
        <h1>Unified Kubernetes Control Plane</h1>
        <p>
          Crelan integrates 52+ specialized Kubernetes administrative tools, Google-standard SRE radars, enterprise security probers, and multi-cluster fleet controls directly in your web browser. Zero heavy desktop clients, zero external plugins, zero license lock-in.
        </p>
      </div>
      <div style="display:flex; flex-direction:column; gap:8px; align-items:flex-end;">
        <div style="display:flex; gap:6px; flex-wrap:wrap;">
          <span class="admin-docs-pill" style="background:#e0f2fe; color:#0369a1; border-color:#bae6fd; font-weight:700;">52 Platform Components</span>
          <span class="admin-docs-pill" style="background:#fef3c7; color:#92400e; border-color:#fde68a; font-weight:700;">4 Operational Domains</span>
          <span class="admin-docs-pill" style="background:#ecfdf5; color:#065f46; border-color:#a7f3d0; font-weight:700;">100% Web-Native</span>
        </div>
        <div style="font-size:11px; color:var(--admin-text-muted);">
          Compare against desktop alternatives: <a href="javascript:void(0)" id="docs-hero-matrix-link" style="color:var(--admin-accent); font-weight:600; text-decoration:underline;">View Lens Comparison Matrix →</a>
        </div>
      </div>
    </div>

    <!-- Domain Tabs -->
    <div class="admin-docs-tabs" id="docs-domain-tabs">
      <button class="admin-docs-tab-btn ${docsActiveTab === 'all' ? 'active' : ''}" data-tab="all" type="button">
        All Components <span style="font-size:10px; opacity:0.8; margin-left:3px;">(52)</span>
      </button>
      <button class="admin-docs-tab-btn ${docsActiveTab === 'k8s' ? 'active' : ''}" data-tab="k8s" type="button">
        ☸️ Cluster Core <span style="font-size:10px; opacity:0.8; margin-left:3px;">(17)</span>
      </button>
      <button class="admin-docs-tab-btn ${docsActiveTab === 'sre' ? 'active' : ''}" data-tab="sre" type="button">
        📈 SRE &amp; Reliability <span style="font-size:10px; opacity:0.8; margin-left:3px;">(10)</span>
      </button>
      <button class="admin-docs-tab-btn ${docsActiveTab === 'security' ? 'active' : ''}" data-tab="security" type="button">
        🛡️ Security &amp; Compliance <span style="font-size:10px; opacity:0.8; margin-left:3px;">(8)</span>
      </button>
      <button class="admin-docs-tab-btn ${docsActiveTab === 'ops' ? 'active' : ''}" data-tab="ops" type="button">
        ⚙️ Platform Ops &amp; Fleet <span style="font-size:10px; opacity:0.8; margin-left:3px;">(17)</span>
      </button>
      <button class="admin-docs-tab-btn ${docsActiveTab === 'lens-matrix' ? 'active' : ''}" data-tab="lens-matrix" type="button" style="border-left:2px solid var(--admin-accent);">
        Lens vs Crelan Matrix <span style="font-size:10px; background:var(--admin-accent); color:#fff; padding:1px 5px; border-radius:10px; margin-left:3px;">16</span>
      </button>
    </div>

    <!-- Search & Accordion Controls -->
    <div style="display: flex; gap: 10px; align-items: center; margin-bottom: 20px; flex-wrap: wrap;">
      <div class="admin-docs-search-bar" style="flex: 1; margin-bottom: 0;">
        <span class="admin-svg-icon" style="color: var(--admin-text-muted);">${getSvgIcon("search", 15)}</span>
        <input class="admin-docs-search-input" id="docs-search-input" type="search" placeholder="Search components by name, domain, capability, or keyword... (e.g. SLO, Chaos, Post-Mortem, CVE, PVC, Drift)" value="${escapeHTML(docsSearchQuery)}" autocomplete="off">
        <span id="docs-count-badge" style="font-size:11px; color:var(--admin-text-muted); font-weight:600; font-family:var(--admin-mono);"></span>
      </div>
      <button class="admin-btn admin-btn-sm" type="button" id="docs-toggle-accordions-btn" onclick="toggleAllDocsAccordions()" style="white-space: nowrap; display: inline-flex; align-items: center; gap: 6px;">
        ${getSvgIcon("layers", 13)} Expand All
      </button>
    </div>

    <!-- Dynamic Container -->
    <div id="docs-content-container"></div>
  `;

  // Attach tab switching events
  const tabs = $$("#docs-domain-tabs .admin-docs-tab-btn");
  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      tabs.forEach((t) => t.classList.remove("active"));
      tab.classList.add("active");
      docsActiveTab = tab.dataset.tab;
      updateDocsContent();
    });
  });

  // Attach search input event
  const searchInput = $("#docs-search-input");
  if (searchInput) {
    searchInput.addEventListener("input", (e) => {
      docsSearchQuery = e.target.value.trim().toLowerCase();
      updateDocsContent();
    });
  }

  // Quick matrix shortcuts
  const openMatrixBtn = $("#docs-open-matrix-btn");
  if (openMatrixBtn) {
    openMatrixBtn.addEventListener("click", () => {
      const matrixTab = $("#docs-domain-tabs button[data-tab='lens-matrix']");
      if (matrixTab) matrixTab.click();
    });
  }
  const heroMatrixLink = $("#docs-hero-matrix-link");
  if (heroMatrixLink) {
    heroMatrixLink.addEventListener("click", () => {
      const matrixTab = $("#docs-domain-tabs button[data-tab='lens-matrix']");
      if (matrixTab) matrixTab.click();
    });
  }

  updateDocsContent();
}

function updateDocsContent() {
  const container = $("#docs-content-container");
  const countBadge = $("#docs-count-badge");
  if (!container) return;

  if (docsActiveTab === "lens-matrix") {
    // Render Lens vs Crelan Comparison Matrix
    const query = docsSearchQuery;
    const filteredRows = LENS_VS_CRELAN_MATRIX.filter((item) => {
      if (!query) return true;
      return (
        item.category.toLowerCase().includes(query) ||
        item.feature.toLowerCase().includes(query) ||
        item.lens.toLowerCase().includes(query) ||
        item.crelan.toLowerCase().includes(query) ||
        item.impact.toLowerCase().includes(query)
      );
    });

    if (countBadge) countBadge.textContent = `${filteredRows.length} comparisons`;

    container.innerHTML = `
      <div style="background:#ffffff; border:1px solid var(--admin-border); border-radius:10px; padding:20px; margin-bottom:20px; box-shadow:var(--admin-shadow-sm);">
        <div style="display:flex; justify-content:space-between; align-items:flex-start; gap:16px; flex-wrap:wrap; margin-bottom:14px;">
          <div>
            <h2 style="margin:0 0 4px; font-size:18px; font-weight:700; color:var(--admin-text);">Why Engineering Teams Choose Crelan Over Lens</h2>
            <p style="margin:0; font-size:13px; color:var(--admin-text-muted); max-width:820px;">
              Lens is traditionally distributed as an Electron desktop client requiring individual workstation installs, local kubeconfig access, and separate paid subscriptions for security or cloud features. Crelan is a <strong>100% web-native, multi-tenant cluster management platform</strong> equipped with native SRE automation, automated post-mortem generation, guarded chaos drills, and continuous vulnerability scanning.
            </p>
          </div>
          <span class="admin-docs-pill" style="background:#f0fdf4; color:#166534; border-color:#bbf7d0; font-weight:700; font-size:12px; padding:4px 10px;">
            16 Structural Advantages
          </span>
        </div>
      </div>

      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width:160px;">Domain</th>
              <th style="width:200px;">Capability</th>
              <th style="width:280px; color:#64748b;">Lens (Desktop Electron)</th>
              <th style="width:340px; color:#166534; background:#f0fdf4;">Crelan Platform Console</th>
              <th>Platform &amp; SRE Advantage</th>
            </tr>
          </thead>
          <tbody>
            ${
              filteredRows.length === 0
                ? `<tr><td colspan="5" style="text-align:center; padding:40px; color:var(--admin-text-muted);">No comparison features match your search.</td></tr>`
                : filteredRows
                    .map(
                      (row) => `
              <tr>
                <td><span class="admin-docs-pill" style="font-size:10.5px;">${escapeHTML(row.category)}</span></td>
                <td><strong style="color:var(--admin-text); font-size:13px;">${escapeHTML(row.feature)}</strong></td>
                <td style="color:#64748b; font-size:12px; line-height:1.5;">${escapeHTML(row.lens)}</td>
                <td style="background:#f0fdf4; color:#166534; font-size:12.5px; font-weight:600; line-height:1.5;">
                  <div style="display:flex; align-items:flex-start; gap:6px;">
                    <span style="color:#16a34a; font-weight:700;">[+]</span>
                    <span>${escapeHTML(row.crelan)}</span>
                  </div>
                </td>
                <td style="font-size:12px; color:#334155; line-height:1.5;">${escapeHTML(row.impact)}</td>
              </tr>
            `
                    )
                    .join("")
            }
          </tbody>
        </table>
      </div>
    `;
    return;
  }

  // Filter components
  const query = docsSearchQuery;
  const filtered = ADMIN_COMPONENTS.filter((comp) => {
    if (docsActiveTab !== "all" && comp.domain !== docsActiveTab) {
      return false;
    }
    if (!query) return true;
    const matchTitle = comp.title.toLowerCase().includes(query);
    const matchDesc = comp.desc.toLowerCase().includes(query);
    const matchDomain = comp.domainLabel.toLowerCase().includes(query);
    const matchRoute = comp.route.toLowerCase().includes(query);
    const matchFeatures = comp.features.some((f) => f.toLowerCase().includes(query));
    return matchTitle || matchDesc || matchDomain || matchRoute || matchFeatures;
  });

  if (countBadge) countBadge.textContent = `${filtered.length} components`;

  if (filtered.length === 0) {
    container.innerHTML = `
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 60px 20px;">
          <div style="font-size:13px; font-weight:700; color:var(--admin-text-muted); margin-bottom:8px;">NO MATCHES</div>
          <h2 style="font-size:16px; margin-bottom:6px;">No components match "${escapeHTML(query)}"</h2>
          <p style="color:var(--admin-text-muted); font-size:13px; margin:0 0 16px;">Try searching for a different keyword or switch to the "All Components" tab.</p>
          <button class="admin-btn admin-btn-sm" type="button" onclick="document.getElementById('docs-search-input').value=''; docsSearchQuery=''; updateDocsContent();">Clear search filter</button>
        </div>
      </div>
    `;
    return;
  }

  container.innerHTML = `
    <div class="admin-docs-grid">
      ${filtered
        .map((comp) => {
          let badgeStyle = "background:#f1f5f9; color:#475569; border-color:#cbd5e1;";
          if (comp.badge === "Crelan Exclusive") {
            badgeStyle = "background:#fef2f2; color:#b91c1c; border-color:#fecaca; font-weight:700;";
          } else if (comp.badge === "Enterprise") {
            badgeStyle = "background:#eff6ff; color:#1d4ed8; border-color:#bfdbfe; font-weight:700;";
          } else if (comp.badge === "AI Native") {
            badgeStyle = "background:#faf5ff; color:#7e22ce; border-color:#e9d5ff; font-weight:700;";
          } else if (comp.badge === "FinOps") {
            badgeStyle = "background:#f0fdf4; color:#15803d; border-color:#bbf7d0; font-weight:700;";
          }

          const actionBtn =
            comp.route === "#cmd-k"
              ? `<button class="admin-btn admin-btn-sm admin-btn-primary" type="button" onclick="openCommandPalette()">Launch Quick Launcher</button>`
              : `<button class="admin-btn admin-btn-sm admin-btn-primary" type="button" onclick="setRoute('${comp.route}')">Launch View →</button>`;

          return `
          <div class="admin-docs-card">
            <div class="admin-docs-card-header">
              <div class="admin-docs-card-title">
                <div class="admin-docs-card-pictogram">
                  ${getSvgIcon(comp.pictogram || "cube", 18)}
                </div>
                <div>
                  <h3 style="font-size:14.5px; margin:0 0 2px;">${escapeHTML(comp.title)}</h3>
                  <span style="font-size:11px; color:var(--admin-text-muted); font-weight:600;">${escapeHTML(comp.domainLabel)}</span>
                </div>
              </div>
              <span class="admin-docs-pill" style="${badgeStyle}">${escapeHTML(comp.badge)}</span>
            </div>

            <p class="admin-docs-text">${escapeHTML(comp.desc)}</p>

            <details class="admin-docs-accordion" id="docs-accordion-${comp.route}">
              <summary class="admin-docs-summary">
                <span class="admin-docs-summary-title">
                  ${getSvgIcon("layers", 13)}
                  Key Capabilities (${comp.features.length})
                </span>
                ${getSvgIcon("chevron-down", 14)}
              </summary>
              <div class="admin-docs-accordion-body">
                ${comp.features.map(f => `
                  <div class="admin-docs-capability-item">
                    <span class="admin-docs-capability-icon">${getSvgIcon("check", 13)}</span>
                    <span>${escapeHTML(f)}</span>
                  </div>
                `).join("")}
              </div>
            </details>

            <div class="admin-docs-card-footer">
              <span class="admin-docs-pill" style="color:var(--admin-text-muted);">#${escapeHTML(comp.route)}</span>
              ${actionBtn}
            </div>
          </div>
        `;
        })
        .join("")}
    </div>
  `;
}

async function initializeAdmin() {
  try {
    const user = await request("/api/v1/me");
    if (user.platform_admin !== true) {
      $("#admin-content-area").innerHTML = `
        <div class="admin-empty-state">
          <h2>Access Denied</h2>
          <p>Your account does not have platform administrator access.</p>
          <a class="admin-btn admin-btn-primary" href="/dashboard">Return to Customer Workspace</a>
        </div>`;
      return;
    }
    const name = user.name || user.email || "Admin";
    $("#admin-avatar").textContent = name.charAt(0).toUpperCase();

    setupEventListeners();
    await Promise.all([loadClusterHeader(), loadNamespacesDropdown(), loadWorkspaceMappings()]);
    await renderCurrentView();

    // Auto-refresh is disabled by default; only start if select value > 0
    if (state.autoRefreshTimer) {
      clearInterval(state.autoRefreshTimer);
      state.autoRefreshTimer = null;
    }
    const refreshSelect = $("#admin-auto-refresh-select");
    const refreshMs = refreshSelect ? parseInt(refreshSelect.value, 10) : 0;
    if (refreshMs > 0) {
      state.autoRefreshTimer = setInterval(() => {
        if (shouldAutoRefresh()) {
          loadClusterHeader();
          renderCurrentView();
        }
      }, refreshMs);
    }
  } catch (err) {
    if (err.message !== "Sign in required") {
      $("#admin-content-area").innerHTML = `<div class="admin-empty-state">Failed to initialize: ${escapeHTML(err.message)}</div>`;
    }
  }
}

initializeAdmin();
