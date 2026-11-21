#include "server/util.h"

#include "server/projects.h"
#include "server/state.h"

#include <algorithm>
#include <cctype>
#include <charconv>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <mutex>
#include <random>
#include <sys/stat.h>

namespace zs {

bool b64_decode_strict(const std::string & raw, std::vector<uint8_t> & out) {
    std::string payload = strip(raw);
    if (payload.rfind("data:", 0) == 0) {
        const size_t comma = payload.find(',');
        if (comma == std::string::npos) return false;
        payload = payload.substr(comma + 1);
    }
    static int8_t T[256];
    static std::once_flag once;
    std::call_once(once, [] {
        for (int i = 0; i < 256; ++i) T[i] = -1;
        const char * A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        for (int i = 0; i < 64; ++i) T[(unsigned char) A[i]] = (int8_t) i;
    });
    size_t data_len = payload.size();
    while (data_len > 0 && payload[data_len - 1] == '=') --data_len;
    const size_t pad = payload.size() - data_len;
    if (pad > 2 || payload.size() % 4 != 0) return false;
    out.clear();
    out.reserve(data_len * 3 / 4);
    uint32_t val = 0;
    int bits = 0;
    for (size_t i = 0; i < data_len; ++i) {
        const int8_t d = T[(unsigned char) payload[i]];
        if (d < 0) return false;
        val = (val << 6) | (uint32_t) d;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            out.push_back((uint8_t) ((val >> bits) & 0xFF));
        }
    }
    return true;
}

std::string b64_encode(const uint8_t * d, size_t n) {
    static const char * A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    out.reserve((n + 2) / 3 * 4);
    size_t i = 0;
    for (; i + 2 < n; i += 3) {
        const uint32_t v = (uint32_t) d[i] << 16 | (uint32_t) d[i + 1] << 8 | d[i + 2];
        out += A[v >> 18];
        out += A[(v >> 12) & 63];
        out += A[(v >> 6) & 63];
        out += A[v & 63];
    }
    if (i < n) {
        uint32_t v = (uint32_t) d[i] << 16;
        if (i + 1 < n) v |= (uint32_t) d[i + 1] << 8;
        out += A[v >> 18];
        out += A[(v >> 12) & 63];
        out += i + 1 < n ? A[(v >> 6) & 63] : '=';
        out += '=';
    }
    return out;
}

// ------------------------------------------------------------------------------------------------ SHA-256

static inline uint32_t rotr(uint32_t x, int n) {
    return (x >> n) | (x << (32 - n));
}

static const uint32_t K256[64] = {
    0x428a2f98,
    0x71374491,
    0xb5c0fbcf,
    0xe9b5dba5,
    0x3956c25b,
    0x59f111f1,
    0x923f82a4,
    0xab1c5ed5,
    0xd807aa98,
    0x12835b01,
    0x243185be,
    0x550c7dc3,
    0x72be5d74,
    0x80deb1fe,
    0x9bdc06a7,
    0xc19bf174,
    0xe49b69c1,
    0xefbe4786,
    0x0fc19dc6,
    0x240ca1cc,
    0x2de92c6f,
    0x4a7484aa,
    0x5cb0a9dc,
    0x76f988da,
    0x983e5152,
    0xa831c66d,
    0xb00327c8,
    0xbf597fc7,
    0xc6e00bf3,
    0xd5a79147,
    0x06ca6351,
    0x14292967,
    0x27b70a85,
    0x2e1b2138,
    0x4d2c6dfc,
    0x53380d13,
    0x650a7354,
    0x766a0abb,
    0x81c2c92e,
    0x92722c85,
    0xa2bfe8a1,
    0xa81a664b,
    0xc24b8b70,
    0xc76c51a3,
    0xd192e819,
    0xd6990624,
    0xf40e3585,
    0x106aa070,
    0x19a4c116,
    0x1e376c08,
    0x2748774c,
    0x34b0bcb5,
    0x391c0cb3,
    0x4ed8aa4a,
    0x5b9cca4f,
    0x682e6ff3,
    0x748f82ee,
    0x78a5636f,
    0x84c87814,
    0x8cc70208,
    0x90befffa,
    0xa4506ceb,
    0xbef9a3f7,
    0xc67178f2,
};

Sha256::Sha256() {
    const uint32_t init[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
                              0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
    memcpy(h_, init, sizeof(h_));
}

void Sha256::block(const uint8_t * p) {
    uint32_t w[64];
    for (int i = 0; i < 16; ++i)
        w[i] = (uint32_t) p[4 * i] << 24 | (uint32_t) p[4 * i + 1] << 16 | (uint32_t) p[4 * i + 2] << 8 | p[4 * i + 3];
    for (int i = 16; i < 64; ++i) {
        const uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
        const uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    uint32_t a = h_[0], b = h_[1], c = h_[2], d = h_[3], e = h_[4], f = h_[5], g = h_[6], h = h_[7];
    for (int i = 0; i < 64; ++i) {
        const uint32_t t1 = h + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + K256[i] + w[i];
        const uint32_t t2 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c));
        h = g;
        g = f;
        f = e;
        e = d + t1;
        d = c;
        c = b;
        b = a;
        a = t1 + t2;
    }
    h_[0] += a;
    h_[1] += b;
    h_[2] += c;
    h_[3] += d;
    h_[4] += e;
    h_[5] += f;
    h_[6] += g;
    h_[7] += h;
}

void Sha256::update(const void * data, size_t n) {
    const uint8_t * p = (const uint8_t *) data;
    total_ += n;
    if (len_) {
        const size_t take = std::min(n, (size_t) 64 - len_);
        memcpy(buf_ + len_, p, take);
        len_ += take;
        p += take;
        n -= take;
        if (len_ < 64) return;
        block(buf_);
        len_ = 0;
    }
    for (; n >= 64; p += 64, n -= 64) block(p);
    memcpy(buf_, p, n);
    len_ = n;
}

std::string Sha256::hex() {
    const uint64_t bits = total_ * 8;
    const uint8_t one = 0x80, zero = 0;
    update(&one, 1);
    while (len_ != 56) update(&zero, 1);
    uint8_t lenb[8];
    for (int i = 0; i < 8; ++i) lenb[i] = (uint8_t) (bits >> (56 - 8 * i));
    update(lenb, 8);
    static const char * D = "0123456789abcdef";
    std::string out;
    for (uint32_t v : h_)
        for (int s = 28; s >= 0; s -= 4) out += D[(v >> s) & 15];
    return out;
}

// ------------------------------------------------------------------------------------------------ SHA-1

std::string sha1_hex(const std::string & data) {
    uint32_t h[5] = {0x67452301, 0xEFCDAB89, 0x98BADCFE, 0x10325476, 0xC3D2E1F0};
    std::string msg = data;
    const uint64_t bits = (uint64_t) data.size() * 8;
    msg += (char) 0x80;
    while (msg.size() % 64 != 56) msg += (char) 0;
    for (int i = 7; i >= 0; --i) msg += (char) (bits >> (8 * i));
    auto rol = [](uint32_t x, int n) { return (x << n) | (x >> (32 - n)); };
    for (size_t off = 0; off < msg.size(); off += 64) {
        uint32_t w[80];
        const uint8_t * p = (const uint8_t *) msg.data() + off;
        for (int i = 0; i < 16; ++i)
            w[i] = (uint32_t) p[4 * i] << 24 | (uint32_t) p[4 * i + 1] << 16 | (uint32_t) p[4 * i + 2] << 8 | p[4 * i + 3];
        for (int i = 16; i < 80; ++i) w[i] = rol(w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1);
        uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4];
        for (int i = 0; i < 80; ++i) {
            uint32_t f, k;
            if (i < 20) {
                f = (b & c) | (~b & d);
                k = 0x5A827999;
            } else if (i < 40) {
                f = b ^ c ^ d;
                k = 0x6ED9EBA1;
            } else if (i < 60) {
                f = (b & c) | (b & d) | (c & d);
                k = 0x8F1BBCDC;
            } else {
                f = b ^ c ^ d;
                k = 0xCA62C1D6;
            }
            const uint32_t t = rol(a, 5) + f + e + k + w[i];
            e = d;
            d = c;
            c = rol(b, 30);
            b = a;
            a = t;
        }
        h[0] += a;
        h[1] += b;
        h[2] += c;
        h[3] += d;
        h[4] += e;
    }
    static const char * D = "0123456789abcdef";
    std::string out;
    for (uint32_t v : h)
        for (int s = 28; s >= 0; s -= 4) out += D[(v >> s) & 15];
    return out;
}

// ------------------------------------------------------------------------------------------------ misc

bool read_file(const std::filesystem::path & path, std::string & out) {
    FILE * f = fopen(path.string().c_str(), "rb");
    if (!f) return false;
    out.clear();
    char buf[65536];
    size_t n;
    while ((n = fread(buf, 1, sizeof(buf), f)) > 0) out.append(buf, n);
    const bool ok = !ferror(f);
    fclose(f);
    return ok;
}

double unix_now() {
    return std::chrono::duration<double>(std::chrono::system_clock::now().time_since_epoch()).count();
}

double file_mtime(const std::filesystem::path & p) {
#ifdef _WIN32
    struct _stat64 st;
    if (_stat64(p.string().c_str(), &st) != 0) return 0.0;
    return (double) st.st_mtime;
#elif defined(__APPLE__)
    struct stat st;
    if (stat(p.string().c_str(), &st) != 0) return 0.0;
    return (double) st.st_mtimespec.tv_sec + st.st_mtimespec.tv_nsec * 1e-9;
#else
    struct stat st;
    if (stat(p.string().c_str(), &st) != 0) return 0.0;
    return (double) st.st_mtim.tv_sec + st.st_mtim.tv_nsec * 1e-9;
#endif
}

std::string random_hex(size_t n) {
    static std::mutex mtx;
    static std::mt19937_64 rng{std::random_device{}()};
    static const char * D = "0123456789abcdef";
    std::lock_guard<std::mutex> lk(mtx);
    std::string out;
    while (out.size() < n) {
        uint64_t v = rng();
        for (int i = 0; i < 16 && out.size() < n; ++i, v >>= 4) out += D[v & 15];
    }
    return out;
}

std::string lower_ascii(std::string s) {
    for (char & c : s) c = (char) std::tolower((unsigned char) c);
    return s;
}

std::string strip(const std::string & s) {
    size_t begin = 0, end = s.size();
    for (size_t i = 0; i < s.size();) {
        const size_t at = i;
        if (!is_py_space(utf8_next(s, i))) {
            begin = at;
            break;
        }
        begin = i;
    }
    // walk from the end: find the last non-space code point
    size_t last_end = begin;
    for (size_t i = begin; i < s.size();) {
        if (!is_py_space(utf8_next(s, i))) last_end = i;
    }
    end = last_end;
    return s.substr(begin, end - begin);
}

std::string py_repr(const std::string & s) {
    const bool single = s.find('\'') == std::string::npos || s.find('"') != std::string::npos;
    const char q = single ? '\'' : '"';
    std::string out(1, q);
    for (char c : s) {
        if (c == '\\') out += "\\\\";
        else if (c == q) {
            out += '\\';
            out += c;
        } else if (c == '\n') out += "\\n";
        else if (c == '\t') out += "\\t";
        else if (c == '\r') out += "\\r";
        else out += c;
    }
    return out + q;
}

// In the library (not main.cpp) so zonos3-cli --request can hold a ServerState.
ServerState::~ServerState() {
    decoders.clear(); // each frees its lanes, then its weights
    zac_encoder_free(encoder);
    ssl_free(teacher);
    spk_free(spk);
    model.accents = nullptr;
    accent_adapters.reset(); // the adapter bank lives on the backbone's backend
    zonos3_model_free(model);
}

double float_decimal(float v) {
    char buf[64];
    const auto r = std::to_chars(buf, buf + sizeof buf - 1, v);
    *r.ptr = 0;
    return strtod(buf, nullptr);
}

} // namespace zs
