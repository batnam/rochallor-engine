import { type ReactNode, useEffect, useMemo, useState } from "react";

import { IncidentsRoute } from "./routes/IncidentsRoute";
import type { Navigation } from "./routes/Navigation";
import { ProcessInstanceRoute } from "./routes/ProcessInstanceRoute";
import { ProcessInstancesRoute } from "./routes/ProcessInstancesRoute";

interface Location {
  pathname: string;
  search: string;
}

function browserLocation(): Location {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
  };
}

export function App(): ReactNode {
  const [location, setLocation] = useState(browserLocation);
  const [sidebarOpen, setSidebarOpen] = useState(
    () => window.innerWidth >= 1024,
  );
  const navigation = useMemo<Navigation>(
    () => ({
      back: () => window.history.back(),
      push: (target) => {
        window.history.pushState(null, "", target);
        setLocation(browserLocation());
      },
    }),
    [],
  );

  useEffect(() => {
    const restoreFromUrl = (): void => setLocation(browserLocation());
    window.addEventListener("popstate", restoreFromUrl);
    return () => window.removeEventListener("popstate", restoreFromUrl);
  }, []);

  const processInstanceMatch = /^\/process-instances\/([^/]+)$/.exec(
    location.pathname,
  );
  const incidentMatch = /^\/incidents\/([^/]+)$/.exec(location.pathname);
  let content: ReactNode;
  if (location.pathname === "/") {
    content = (
      <ProcessInstancesRoute navigation={navigation} search={location.search} />
    );
  } else if (processInstanceMatch) {
    const instanceId = decodeURIComponent(processInstanceMatch[1]);
    content = (
      <ProcessInstanceRoute
        instanceId={instanceId}
        key={instanceId}
        search={location.search}
      />
    );
  } else if (location.pathname === "/incidents") {
    content = (
      <IncidentsRoute navigation={navigation} search={location.search} />
    );
  } else if (incidentMatch) {
    content = (
      <IncidentsRoute
        incidentId={decodeURIComponent(incidentMatch[1])}
        navigation={navigation}
        search={location.search}
      />
    );
  } else {
    content = (
      <main className="rm-page">
        <section className="rm-card rm-state-card rm-state-card--error">
          <h1>Not Found</h1>
          <p>The requested Monitor view does not exist.</p>
        </section>
      </main>
    );
  }

  const incidentsActive = location.pathname.startsWith("/incidents");
  const processInstancesActive =
    location.pathname === "/" ||
    location.pathname.startsWith("/process-instances");
  const detailId = location.pathname.split("/")[2];

  return (
    <div className="rm-shell">
      <header className="rm-topbar">
        <button
          aria-expanded={sidebarOpen}
          aria-label={sidebarOpen ? "Collapse sidebar" : "Expand sidebar"}
          className="rm-icon-button"
          onClick={() => setSidebarOpen((current) => !current)}
          type="button"
        >
          <svg aria-hidden="true" viewBox="0 0 24 24">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <div>
          <span className="rm-eyebrow">Operations</span>
          <h1>Rochallor Monitor</h1>
        </div>
      </header>
      <div className="rm-layout">
        <aside
          className={`rm-sidebar${sidebarOpen ? " rm-sidebar--open" : ""}`}
        >
          <span className="rm-sidebar-label">Monitoring</span>
          <nav aria-label="Monitor sections" className="rm-sidebar-nav">
            <a
              aria-current={processInstancesActive ? "page" : undefined}
              href="/"
              onClick={(event) => {
                event.preventDefault();
                navigation.push("/");
              }}
            >
              <span aria-hidden="true" className="rm-nav-marker" />
              Process Instances
            </a>
            <a
              aria-current={incidentsActive ? "page" : undefined}
              href="/incidents"
              onClick={(event) => {
                event.preventDefault();
                navigation.push("/incidents");
              }}
            >
              <span aria-hidden="true" className="rm-nav-marker" />
              Incidents
            </a>
          </nav>
          <div className="rm-sidebar-note">
            Read-only operational visibility
          </div>
        </aside>
        <div className="rm-workspace">
          <nav aria-label="Breadcrumb" className="rm-breadcrumb">
            <a
              href={incidentsActive ? "/incidents" : "/"}
              onClick={(event) => {
                event.preventDefault();
                navigation.push(incidentsActive ? "/incidents" : "/");
              }}
            >
              {incidentsActive ? "Incidents" : "Process Instances"}
            </a>
            {detailId ? (
              <>
                <span aria-hidden="true">/</span>
                <span title={decodeURIComponent(detailId)}>
                  {decodeURIComponent(detailId)}
                </span>
              </>
            ) : null}
          </nav>
          {content}
        </div>
      </div>
    </div>
  );
}
