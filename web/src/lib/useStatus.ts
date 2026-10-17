import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { fetchStatus } from './api';
import type { StatusFeed } from './types';

// The dashboard polls hard enough to feel live while the user is looking at it,
// and backs off to a minute once it is idle or hidden — the behaviour the
// original jQuery front end implements.
function interval(active: boolean, visible: boolean): number {
  if (!visible) return 60_000;
  return active ? 3_000 : 15_000;
}

export function useStatusFeed() {
  const etag = useRef<string | null>(null);
  const [feed, setFeed] = useState<StatusFeed | null>(null);
  const [active, setActive] = useState(true);
  const [visible, setVisible] = useState(!document.hidden);

  useEffect(() => {
    const onMove = () => setActive(true);
    const onVisibility = () => setVisible(!document.hidden);
    window.addEventListener('pointermove', onMove);
    window.addEventListener('keydown', onMove);
    document.addEventListener('visibilitychange', onVisibility);
    const idle = setInterval(() => setActive(false), 60_000);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('keydown', onMove);
      document.removeEventListener('visibilitychange', onVisibility);
      clearInterval(idle);
    };
  }, []);

  const query = useQuery({
    queryKey: ['status'],
    queryFn: async () => {
      const result = await fetchStatus(etag.current);
      if (result.feed) {
        etag.current = result.etag;
        setFeed(result.feed);
        // A 304 means nothing changed, so the next tick can relax.
        return { changed: true };
      }
      etag.current = result.etag;
      return { changed: false };
    },
    refetchInterval: () => interval(active, visible),
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true
  });

  useEffect(() => {
    if (feed?.title) document.title = feed.title;
    return () => {
      document.title = '飞檄';
    };
  }, [feed?.title]);

  return { feed, error: query.error };
}
