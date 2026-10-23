// Demo cache stampede pada /api/cache/products/popular.
//
// Jalankan server dengan POPULAR_TTL pendek supaya key sering kedaluwarsa:
//   POPULAR_TTL=5s STAMPEDE_PROTECT=false ./demo   # tanpa proteksi
//   POPULAR_TTL=5s STAMPEDE_PROTECT=true  ./demo   # singleflight + lock
//
//   k6 run -e BASE_URL=http://[ip-vps]:8080 k6/stampede.js
//
// Bandingkan cache.db_popular_queries di /debug/stats dan p99 di ringkasan k6.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    homepage: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 200),
      duration: __ENV.DURATION || '40s',
    },
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  http.post(`${__ENV.BASE_URL}/debug/stats/reset`);
}

export default function () {
  const res = http.get(`${__ENV.BASE_URL}/api/cache/products/popular`, { timeout: '30s' });
  check(res, { 'status 200': (r) => r.status === 200 });
}

export function teardown() {
  const s = http.get(`${__ENV.BASE_URL}/debug/stats`).json().cache;
  console.log(`db_popular_queries=${s.db_popular_queries} singleflight_dup=${s.singleflight_dup} lock_waits=${s.lock_waits}`);
}
