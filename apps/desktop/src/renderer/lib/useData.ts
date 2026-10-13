// Loads something over the bridge, keeps the last good value while it reloads, and reloads on an
// interval and whenever the monitor reports a change (a new alert, a state poll).
import { useCallback, useEffect, useRef, useState } from "react";
import { BridgeError } from "./bridge";
import { useApp } from "./app";

export interface DataState<T> {
  data: T | null;
  error: BridgeError | null;
  loading: boolean;
  reload: () => void;
}

export function useData<T>(load: () => Promise<T>, deps: unknown[], opts: { every?: number; onMonitor?: boolean } = {}): DataState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<BridgeError | null>(null);
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);
  const { monitor } = useApp();
  const stamp = opts.onMonitor ? monitor?.alerts.length : 0;

  const run = useCallback(() => {
    const mine = ++seq.current;
    load()
      .then((d) => {
        if (mine !== seq.current) return;
        setData(d);
        setError(null);
      })
      .catch((e) => {
        if (mine !== seq.current) return;
        setError(e instanceof BridgeError ? e : new BridgeError(0, String(e)));
      })
      .finally(() => {
        if (mine === seq.current) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  useEffect(() => {
    setLoading(true);
    run();
    const id = opts.every ? setInterval(run, opts.every) : null;
    return () => {
      seq.current++;
      if (id) clearInterval(id);
    };
  }, [run, opts.every, stamp]);

  return { data, error, loading, reload: run };
}
