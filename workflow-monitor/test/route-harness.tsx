import { type ReactNode, useEffect, useMemo, useState } from "react";

import type { Navigation } from "../src/routes/Navigation";

interface TestLocation {
  pathname: string;
  search: string;
}

function browserLocation(): TestLocation {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
  };
}

export function RouteHarness({
  children,
}: {
  children: (location: TestLocation, navigation: Navigation) => ReactNode;
}): ReactNode {
  const [location, setLocation] = useState(browserLocation);
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

  return children(location, navigation);
}
