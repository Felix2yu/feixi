// 飞檄的 Service Worker：只给「本次构建的壳 + 静态资源」做秒开，数据接口一律不碰。
// 所以它不承诺离线：断网时打开的是上一次构建的空壳，接口照常报错、界面显示空态，
// 而不是拿旧数据冒充新数据。
//
// __BUILD_VERSION__ 由 Go 在响应 /sw.js 时替换成内嵌 index.html 的指纹。这一步是必需的：
// 浏览器只在脚本字节变化时才装新 SW，而前端发版改的是 hashed 产物、不是这个文件本身。
const BUILD_VERSION = '__BUILD_VERSION__';
const CACHE = 'feixi-' + BUILD_VERSION;
const MAX_ENTRIES = 64;
const DATA_PREFIXES = ['/api/', '/front/', '/ping/', '/badge/', '/b/'];

// 要预缓存什么，问本次构建的入口 HTML：它引用的 hashed 产物与图标就是本次的全部。
async function precacheUrls() {
  const html = await (await fetch('/', { cache: 'reload' })).text();
  const refs = [
    ...html.matchAll(
      /\/(?:assets\/[^"']+|icon-[^"']+|favicon\.\w+|apple-touch-icon\.png|manifest\.webmanifest)/g
    )
  ].map((m) => m[0]);
  return [...new Set(['/'].concat(refs))].slice(0, MAX_ENTRIES);
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      await Promise.all((await precacheUrls()).map((u) => cache.add(u).catch(() => {})));
      self.skipWaiting();
    })()
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      for (const key of await caches.keys()) {
        if (key.startsWith('feixi-') && key !== CACHE) await caches.delete(key);
      }
      await self.clients.claim();
    })()
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin || DATA_PREFIXES.some((p) => url.pathname.startsWith(p)))
    return;

  if (req.mode === 'navigate') {
    event.respondWith(fetch(req).catch(() => caches.match('/')));
  } else if (url.pathname.startsWith('/assets/')) {
    event.respondWith(caches.match(req).then((hit) => hit || fetch(req)));
  }
});
