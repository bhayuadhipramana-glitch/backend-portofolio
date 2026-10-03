# PRD: Backend Portofolio

Oct 3, 2026 · @Brian Arta Winata

## Ringkasan

Produk ini adalah backend portofolio berbasis Go yang membuktikan performanya dengan angka, bukan klaim: p99 baca di bawah 50 ms dan 5.000 RPS pada satu node, diukur otomatis di CI dan ditampilkan langsung ke pengunjung. Dokumen ini hanya mencakup backend, database, dan infrastruktur; frontend dibahas terpisah.

| Keputusan | Pilihan | Alasan |
| --- | --- | --- |
| Hosting | Hybrid: homelab Proxmox (k3s) sebagai runtime utama, Azure untuk backup off-site | Homelab tidak memakan biaya bulanan dan menunjukkan kemampuan infrastruktur. Kredit Azure US$100 terlalu kecil untuk menjalankan seluruh stack 24 jam. |
| Akses publik | Cloudflare Tunnel dan CDN | Tidak butuh IP publik di rumah. Frontend statis tetap tampil saat homelab mati. |
| SDLC | Hybrid: desain lengkap di depan, bangun bertahap per milestone, uji di setiap milestone | Klaim performa harus diuji sejak awal, tidak bisa ditunda ke akhir. |
| Arsitektur | Modular monolith ditambah satu worker, pola Hexagonal | Cukup untuk beban target, mudah dikerjakan sendirian, tetap bisa dipecah nanti. |
| Data | PostgreSQL 17, Valkey, NATS JetStream | Relasional sebagai sumber kebenaran, cache untuk jalur baca, broker ringan untuk pipeline event. |
| Fitur unggulan | Pipeline event real-time dengan live feed lewat SSE | Menghasilkan beban nyata sehingga performa bisa didemonstrasikan. |

Semua pilihan di luar hosting, SDLC, dan UML memakai jawaban bawaan dari formulir Langkah 1. Pilihan yang masih perlu konfirmasi ada di bagian Pertanyaan terbuka.

## Tujuan dan non-tujuan

Tujuan utamanya satu: engineer senior yang membuka situs atau repo ini selama 60 detik menyimpulkan bahwa pemiliknya mampu merancang sistem yang cepat, teramati, dan bisa dioperasikan. Trafik alami sebuah portofolio sangat kecil, jadi performa harus dibuktikan lewat beban kerja yang sengaja dirancang.

### Tujuan

| ID | Tujuan | Ukuran |
| --- | --- | --- |
| G1 | Menyajikan konten portofolio (proyek, résumé, artikel, kontak) lewat API yang cepat | p99 baca di bawah 50 ms di origin |
| G2 | Menjalankan pipeline event real-time yang bisa dicoba pengunjung | Event tampil di live feed kurang dari 2 detik setelah dikirim |
| G3 | Mempublikasikan bukti performa | Laporan load test, dashboard publik, dan flame graph tersedia di situs |
| G4 | Seluruh infrastruktur terdefinisi sebagai kode | Lingkungan bisa dibangun ulang dari nol dalam 1 jam |
| G5 | Berjalan tanpa biaya langganan cloud bulanan | Pengeluaran Azure tidak melebihi kredit pelajar |

### Non-tujuan versi 1

- Frontend dan desain UI.
- Microservices, multi-region, dan high availability penuh. Sistem berjalan di satu mesin fisik.
- Batch processing, data warehouse, dan orchestrator seperti Airflow.
- Multi-tenant atau pendaftaran pengguna. Hanya ada satu admin.
- Pembayaran atau monetisasi.

## Pengguna sasaran

Ada tiga aktor manusia dan dua aktor sistem; engineer peninjau adalah yang paling menentukan keputusan desain.

| Aktor | Siapa | Kebutuhan utama |
| --- | --- | --- |
| Pengunjung | Recruiter atau hiring manager | Melihat proyek dan résumé dengan cepat di perangkat apa pun, lalu menghubungi pemilik |
| Engineer peninjau | Engineer senior yang menilai kualitas teknis | Melihat bukti: angka latensi, arsitektur, dashboard, kode, dan hasil load test |
| Admin | Pemilik portofolio | Mengelola konten, memantau sistem, dan menjalankan benchmark |
| Load generator | Proses internal | Mengirim event sintetis untuk mengisi pipeline demo |
| CI/CD | GitHub Actions dan Argo CD | Menguji, membangun image, dan men-deploy tanpa akses masuk ke homelab |

## Lingkup fitur versi 1

Versi 1 berisi sembilan fitur; enam berprioritas P0 dan wajib selesai sebelum rilis.

| ID | Fitur | Deskripsi | Prioritas |
| --- | --- | --- | --- |
| F1 | Content API | Endpoint baca untuk profil, proyek, artikel, dan résumé, dengan cache dua lapis | P0 |
| F2 | Admin dan autentikasi | Login GitHub OAuth untuk satu admin, CRUD konten, session di sisi server | P0 |
| F3 | Event ingest | Endpoint menerima event tunggal atau batch, validasi, lalu publish ke NATS JetStream | P0 |
| F4 | Worker agregasi | Konsumen idempoten yang menulis event mentah dan agregat per menit ke PostgreSQL | P0 |
| F5 | Live feed | Stream SSE berisi throughput, latensi, dan agregat terbaru | P0 |
| F6 | Observability | Trace, metrik, dan log lewat OpenTelemetry; dashboard Grafana publik read-only | P0 |
| F7 | Demo burst | Pengunjung memicu ledakan beban terkontrol dan melihat dampaknya di live feed | P1 |
| F8 | Halaman benchmark | Riwayat hasil k6 dari CI dan flame graph pprof | P1 |
| F9 | Formulir kontak | Pesan pengunjung tersimpan dan diteruskan lewat outbox; dibatasi rate limit | P1 |

Ditunda ke versi 2: CDC dengan Debezium, ClickHouse untuk analitik, pencarian teks penuh di luar PostgreSQL, dan failover otomatis ke Azure.

## Metrik keberhasilan dan SLO

Kata "zero-bottleneck" diterjemahkan menjadi delapan target terukur; semuanya diukur di origin, yaitu di dalam jaringan homelab, karena latensi dari internet bergantung pada ISP rumahan.

| Metrik | Target | Cara ukur |
| --- | --- | --- |
| Latensi baca Content API | p50 di bawah 10 ms, p99 di bawah 50 ms | k6 di CI dan histogram OpenTelemetry |
| Latensi tulis ingest | p99 di bawah 150 ms | k6 di CI dan histogram OpenTelemetry |
| Throughput baca | 5.000 RPS selama 5 menit, error di bawah 0,1% | k6 terhadap VM aplikasi |
| Throughput ingest | 2.000 event per detik selama 5 menit tanpa kehilangan event | Load generator dan hitungan baris di PostgreSQL |
| Keterlambatan pipeline | p95 di bawah 2 detik dari ingest sampai tampil di live feed | Selisih timestamp event dan timestamp agregat |
| Cache hit ratio | Minimal 90% pada endpoint konten | Metrik aplikasi |
| Availability | API 99,5% per bulan; frontend statis 99,9% | Pemantauan uptime eksternal |
| Pemulihan | RPO 5 menit; RTO 1 jam untuk kegagalan perangkat lunak, 4 jam untuk kerusakan perangkat keras | Latihan restore bulanan |

Target availability API diturunkan dari 99,9% menjadi 99,5% karena sistem berjalan di satu mesin dengan listrik dan internet rumahan. Angka 99,9% hanya memberi ruang 43 menit mati per bulan, yang tidak realistis tanpa mesin kedua.

## Batasan, asumsi, dan risiko

Batasan terbesar adalah satu mesin fisik di rumah; seluruh mitigasi di bawah dirancang agar situs tetap tampil dan data tidak hilang saat mesin itu mati.

### Pilihan cloud

Azure dipilih karena kreditnya sudah tersedia, bukan karena lebih unggul secara teknis dari AWS atau GCP. Arsitektur ini tidak terikat pada satu cloud: k3s, OpenTofu, dan image kontainer standar bisa dipindahkan ke penyedia mana pun.

Azure for Students memberi kredit US$100 untuk 12 bulan, atau sekitar US$8 per bulan. Subscription dibatalkan saat kredit habis atau 12 bulan berakhir, dan bisa diperpanjang selama masih berstatus mahasiswa ([Microsoft Learn](https://learn.microsoft.com/en-us/azure/education-hub/azure-dev-tools-teaching/azure-students-program)). Karena itu Azure hanya dipakai untuk Blob Storage sebagai tujuan backup, bukan untuk menjalankan aplikasi.

### Batasan dan asumsi

- Satu PC bekas menjalankan Proxmox. Spesifikasinya belum diketahui; dokumen ini mengasumsikan minimal 4 core, 16 GB RAM, dan SSD 256 GB.
- Internet rumahan, kemungkinan tanpa IP publik. Semua akses masuk lewat Cloudflare Tunnel.
- Dikerjakan satu orang dengan bantuan agent di Antigravity.
- Volume data demo 10 juta event, turun dari 50 juta, sampai spesifikasi disk dipastikan.

### Risiko

| Risiko | Dampak | Mitigasi |
| --- | --- | --- |
| Listrik atau internet rumah mati | API tidak bisa diakses | Frontend statis di CDN menampilkan snapshot konten terakhir dan status "demo offline" |
| Disk PC bekas rusak | Data hilang | Arsip WAL dan backup harian ke Azure Blob dengan pgBackRest; restore diuji tiap bulan |
| Kredit Azure habis lebih cepat | Subscription mati, backup berhenti | Hanya memakai Blob Storage; budget alert di US$5 per bulan; salinan backup lokal tetap ada |
| RAM tidak cukup untuk stack observability | Pod terbunuh karena kehabisan memori | Mode single-binary, batas resource per pod, retensi 7 hari |
| Demo burst disalahgunakan | Sistem kelebihan beban | Rate limit di edge dan aplikasi, satu burst aktif dalam satu waktu, durasi maksimal 30 detik |
| Upload ISP kecil | Live feed tersendat saat ramai | Satu pesan SSE per detik, maksimal 200 koneksi SSE bersamaan |

## Metodologi dan milestone

Proyek memakai pendekatan hybrid: requirement dan desain diselesaikan di depan seperti Waterfall, lalu pembangunan berjalan bertahap dalam enam milestone yang masing-masing diuji sebelum lanjut.

Waterfall murni menaruh pengujian di akhir. Untuk proyek yang nilai jualnya adalah performa, itu berisiko: masalah latensi baru ketahuan setelah semua kode selesai dan mahal diperbaiki. Agile murni tanpa desain di depan juga kurang cocok, karena agent di Antigravity bekerja paling baik dengan spesifikasi yang sudah jelas.

| Fase | Isi | Status |
| --- | --- | --- |
| 1. Requirement | PRD dan SRS | Dokumen ini |
| 2. Desain | Diagram use case, activity, sequence, ERD, dan kontrak API | Ada di tab SRS |
| 3. Implementasi | Milestone M0 sampai M5, tiap milestone menghasilkan fitur utuh beserta ujinya | Belum mulai |
| 4. Rilis | Load test penuh, latihan restore, publikasi | Belum mulai |

### Milestone

| Milestone | Minggu | Hasil | Gerbang lulus |
| --- | --- | --- | --- |
| M0 Fondasi | 1 | Proxmox, VM, k3s, repo, CI, Argo CD, Cloudflare Tunnel, satu service contoh | Push ke git otomatis ter-deploy; health check hijau dari internet |
| M1 Content API | 2 sampai 3 | Skema, endpoint baca, cache, login admin, CRUD konten | Integration test lulus; p99 di bawah 50 ms pada 1.000 RPS |
| M2 Observability | 4 | OpenTelemetry, Grafana, k6 di CI | Satu request terlihat utuh sebagai trace; CI gagal jika p99 memburuk lebih dari 20% |
| M3 Pipeline event | 5 sampai 6 | Ingest, NATS JetStream, worker agregasi, outbox, DLQ | 2.000 event per detik selama 5 menit tanpa kehilangan atau duplikasi |
| M4 Live feed | 7 | SSE, demo burst, halaman benchmark | Keterlambatan p95 di bawah 2 detik; burst kedua ditolak saat burst pertama aktif |
| M5 Hardening | 8 | Backup ke Azure Blob, latihan restore, pemindaian keamanan, load test penuh | Semua SLO tercapai; restore dari nol selesai dalam 1 jam |

## Pertanyaan terbuka

Delapan hal ini belum terjawab; tiga yang pertama bisa mengubah angka target di SRS.

- [x] Spesifikasi PC bekas: CPU, RAM, serta jenis dan ukuran disk.
- [x] Koneksi internet rumah: ada IP publik atau tidak, dan berapa kecepatan upload.
- [x] Kredit Azure: sudah aktif atau belum, sisa berapa, dan kapan berakhir.
- [x] Domain demo: tetap event telemetri sintetis, atau data dari bidang yang Anda kuasai.
- [x] Nama domain situs yang akan dipakai.
- [x] Tenggat versi 1: tetap 8 minggu atau tidak.
- [x] Polylane: repo mana yang dimaksud dan perannya dalam alur kerja.
- [x] Setuju atau tidak dengan availability API 99,5%.
