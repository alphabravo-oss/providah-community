import { useEffect, useState } from "react";
import { api, queries } from "./api";
export function useLiveUpdates(organizationId?: string) {
  const [status, setStatus] = useState("Connecting");
  useEffect(() => {
    if (!organizationId) {setStatus("Paused");return;}
    let stopped = false,
      source: EventSource | undefined,
      timer: ReturnType<typeof setTimeout> | undefined;
    const invalidate = () => {
      void queries.invalidateQueries();
    };
    const connect = () => {
      if (stopped) return;
      source = new EventSource(
        `/api/events?organization_id=${encodeURIComponent(organizationId)}`,
      );
      source.onopen = () => setStatus("Live");
      source.addEventListener("change", invalidate);
      source.addEventListener("revoked", () => {
        source?.close();
        setStatus("Access changed");
        queries.clear();
        void api.getSession({}).then(() => window.location.assign("/"));
      });
      const reconnect = () => {
        source?.close();
        clearTimeout(timer);
        setStatus("Reconnecting");
        timer = setTimeout(() => {
          void api
            .getSession({})
            .then(() => {
              invalidate();
              connect();
            })
            .catch(() => {
              if (!stopped) window.location.assign("/");
            });
        }, 3000);
      };
      source.addEventListener("reauth", reconnect);
      source.onerror = reconnect;
    };
    connect();
    return () => {
      stopped = true;
      source?.close();
      clearTimeout(timer);
    };
  }, [organizationId]);
  return status;
}
