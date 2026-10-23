// Simulasi flash sale: banyak user membuka produk promo yang sama.
//
//   k6 run -e BASE_URL=http://[ip-vps]:8080 -e VARIANT=nocache k6/load-test.js
//   k6 run -e BASE_URL=http://[ip-vps]:8080 -e VARIANT=cache   k6/load-test.js
//   k6 run -e BASE_URL=... -e VARIANT=cache -e HOT_SET=500000 k6/load-test.js   # skenario ekor panjang
//
// Closed model (ramping-vus): RPS adalah HASIL kecepatan server,
// bukan beban yang dipaksakan. Lihat k6/arrival-rate.js untuk open model.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  stages: [
    { duration: '20s', target: 50 },  // pemanasan
    { duration: '60s', target: 200 }, // flash sale
    { duration: '10s', target: 0 },
  ],
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const HOT_SET = Number(__ENV.HOT_SET || 100);
const VARIANT = __ENV.VARIANT || 'cache'; // nocache | cache

export default function () {
  const id = Math.floor(Math.random() * HOT_SET) + 1;
  const res = http.get(`${__ENV.BASE_URL}/api/${VARIANT}/products/${id}`);
  check(res, { 'status 200': (r) => r.status === 200 });
}
