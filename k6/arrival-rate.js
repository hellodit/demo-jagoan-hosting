// Open model: request datang dengan laju tetap, tidak peduli server lambat.
// Pertanyaannya: "kuatkah server menerima RATE request per detik?"
//
//   k6 run -e BASE_URL=http://[ip-vps]:8080 -e RATE=1500 -e VARIANT=nocache k6/arrival-rate.js
//   k6 run -e BASE_URL=http://[ip-vps]:8080 -e RATE=1500 -e VARIANT=cache   k6/arrival-rate.js
//
// Rekomendasi untuk demo Go: pakai RATE sedikit DI ATAS kapasitas database
// tanpa cache (cari angkanya saat gladi, lihat ANALISA di README).
import http from 'k6/http';
import { check } from 'k6';

const RATE = Number(__ENV.RATE || 1500);
const HOT_SET = Number(__ENV.HOT_SET || 100);
const VARIANT = __ENV.VARIANT || 'cache'; // nocache | cache

export const options = {
  scenarios: {
    flash_sale: {
      executor: 'ramping-arrival-rate',
      startRate: Math.round(RATE / 10),
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 2000,
      stages: [
        { duration: '20s', target: Math.round(RATE / 3) },
        { duration: '60s', target: RATE },
        { duration: '10s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  const id = Math.floor(Math.random() * HOT_SET) + 1;
  const res = http.get(`${__ENV.BASE_URL}/api/${VARIANT}/products/${id}`, { timeout: '10s' });
  check(res, { 'status 200': (r) => r.status === 200 });
}
