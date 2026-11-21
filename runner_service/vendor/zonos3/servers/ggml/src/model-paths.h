// model-paths.h — companion-GGUF discovery and local converter hints.
// zac-keep2.gguf (the default decoder: the keep2_plain head) / zac-encoder.gguf / ssl-teacher.gguf are conventional
// backbone siblings; when the flags are omitted we look there before giving up. The full-size decoder (full_mr head,
// zac.gguf) is selected with --zac; zonos3-server also serves it as an extra request-selectable decoder when it sits
// next to the backbone.
#pragma once

#include <algorithm>
#include <cstdio>
#include <string>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <limits>
#include <vector>


// The decoder the CLI and server load next to the backbone when --zac is omitted.
static constexpr const char * MP_DEFAULT_ZAC = "zac-keep2.gguf";
// The full-size decoder; zonos3-server serves it next to MP_DEFAULT_ZAC when --zac is omitted and it sits there.
static constexpr const char * MP_FULL_ZAC = "zac.gguf";
static inline bool mp_file_exists(const std::string & p) {
    if (FILE * f = fopen(p.c_str(), "rb")) { fclose(f); return true; }
    return false;
}

static inline std::string mp_parent_dir(const std::string & p) {
    const size_t pos = p.find_last_of("/\\");
    return pos == std::string::npos ? std::string(".") : p.substr(0, pos);
}

// Sibling of `backbone` named `name` if it exists, else "".
static inline std::string mp_find_companion(const std::string & backbone, const char * name) {
    const std::string cand = mp_parent_dir(backbone) + "/" + name;
    return mp_file_exists(cand) ? cand : std::string();
}

static inline void mp_print_missing_hint(const char * name, const std::string & tried) {
    const std::string label(name);
    const char * script = label == MP_FULL_ZAC || label == MP_DEFAULT_ZAC ? "codec-decoder" :
                          label == "zac-encoder.gguf" ? "codec-encoder" :
                          label == "ssl-teacher.gguf" ? "ssl-teacher" :
                          label == "spk-encoder.gguf" ? "speaker-encoder" :
                          label == "steer.gguf" ? "steer" :
                          label == "accents.gguf" ? "accents" :
                          "lm";
    fprintf(stderr, "error: %s not found (tried: %s)\n"
                    "hint: create it with tools/convert_gguf.py %s (see docs/INTERNALS.md) and place it next to the backbone GGUF\n",
            name, tried.c_str(), script);
}

// Ordinary C-order codec references, not delayed backbone token grids. Every frame of the file is validated; codes
// keeps the first max_frames (the registry's reference_max_frames for a reference prefix).
static inline bool mp_load_reference_codes(const std::string & path, std::vector<int32_t> & codes,
                                          std::string & err, int max_frames) {
    codes.clear();
    FILE * f = fopen(path.c_str(), "rb");
    if (!f) { err = "cannot open reference codes: " + path; return false; }
    auto fail = [&](const char * why) { fclose(f); err = why; codes.clear(); return false; };
    unsigned char prefix[8];
    if (fread(prefix, 1, 8, f) != 8 || memcmp(prefix, "\x93NUMPY", 6) ||
        prefix[7] != 0 || (prefix[6] != 1 && prefix[6] != 2 && prefix[6] != 3))
        return fail("invalid NPY reference header");
    unsigned char hl[4] = {};
    const int hbytes = prefix[6] == 1 ? 2 : 4;
    if (fread(hl, 1, hbytes, f) != (size_t) hbytes) return fail("truncated NPY header");
    const uint32_t nheader = hl[0] | ((uint32_t) hl[1] << 8) |
                             ((uint32_t) hl[2] << 16) | ((uint32_t) hl[3] << 24);
    if (nheader > 65536) return fail("oversized NPY reference header");
    std::string header(nheader, '\0');
    if (fread(header.data(), 1, nheader, f) != nheader) return fail("truncated NPY header");
    const size_t order = header.find("fortran_order");
    const size_t shape = header.find("shape");
    const size_t descr = header.find("descr");
    if (order == std::string::npos || shape == std::string::npos || descr == std::string::npos)
        return fail("invalid NPY reference metadata");
    size_t p = header.find(':', order);
    if (p == std::string::npos) return fail("invalid NPY array order");
    p = header.find_first_not_of(" \t", p + 1);
    if (p == std::string::npos || header.compare(p, 5, "False") != 0)
        return fail("reference NPY must be C-order");
    p = header.find(':', descr);
    if (p == std::string::npos) return fail("invalid NPY dtype");
    p = header.find_first_of("'\"", p + 1);
    if (p == std::string::npos) return fail("invalid NPY dtype");
    const size_t end = header.find(header[p], p + 1);
    if (end == std::string::npos) return fail("invalid NPY dtype");
    const std::string dtype = header.substr(p + 1, end - p - 1);
    if (dtype != "<i4" && dtype != "<i8" && dtype != "<u4" && dtype != "<u8" &&
        dtype != "<f4" && dtype != "<f8") return fail("reference NPY needs little-endian i4/i8/u4/u8/f4/f8 dtype");
    p = header.find('(', shape);
    long long frames = 0, width = 0;
    int consumed = 0;
    if (p == std::string::npos ||
        sscanf(header.c_str() + p, "( %lld , %lld %n", &frames, &width, &consumed) != 2 ||
        frames < 1 || frames > std::numeric_limits<long long>::max() / 10 || width != 10)
        return fail("reference codes must have shape [T,10], T >= 1");
    p += consumed;
    while (p < header.size() && (header[p] == ' ' || header[p] == ',')) ++p;
    if (p == header.size() || header[p] != ')') return fail("reference codes must be two-dimensional");
    const int bytes = dtype[2] - '0';
    codes.resize((size_t) std::min<long long>(frames, max_frames) * 10);
    for (long long i = 0; i < frames * 10; ++i) {
        unsigned char raw[8] = {};
        if (fread(raw, 1, bytes, f) != (size_t) bytes) return fail("truncated reference codes");
        uint64_t bits = 0;
        for (int b = 0; b < bytes; ++b) bits |= (uint64_t) raw[b] << (8 * b);
        double value;
        if (dtype[1] == 'f') {
            if (bytes == 4) { uint32_t u = (uint32_t) bits; float v; memcpy(&v, &u, 4); value = v; }
            else { memcpy(&value, &bits, 8); }
        } else if (dtype[1] == 'i') {
            value = bytes == 4 ? (double) (int32_t) bits : (double) (int64_t) bits;
        } else value = (double) bits;
        if (!std::isfinite(value) || value < 0 || value > 4095 || value != std::floor(value))
            return fail("reference codes must be ordinary integer codec IDs 0..4095");
        if (i < (long long) codes.size()) codes[(size_t) i] = (int32_t) value;
    }
    if (fgetc(f) != EOF) return fail("unexpected trailing data in reference NPY");
    fclose(f);
    return true;
}

static inline bool mp_save_reference_codes(const std::string & path,
                                          const std::vector<int32_t> & codes) {
    if (codes.empty() || codes.size() % 10) return false;
    std::string header = "{'descr': '<i4', 'fortran_order': False, 'shape': (" +
                         std::to_string(codes.size() / 10) + ", 10), }";
    header.append((64 - (10 + header.size() + 1) % 64) % 64, ' ');
    header += '\n';
    FILE * f = fopen(path.c_str(), "wb");
    if (!f) return false;
    const unsigned char prefix[10] = {0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0,
                                     (unsigned char) header.size(), (unsigned char) (header.size() >> 8)};
    bool ok = fwrite(prefix, 1, 10, f) == 10 && fwrite(header.data(), 1, header.size(), f) == header.size();
    for (int32_t code : codes) {
        const uint32_t u = (uint32_t) code;
        const unsigned char raw[4] = {(unsigned char) u, (unsigned char) (u >> 8),
                                      (unsigned char) (u >> 16), (unsigned char) (u >> 24)};
        ok = ok && fwrite(raw, 1, 4, f) == 4;
    }
    return fclose(f) == 0 && ok;
}

// Speaker embedding: float32 [192] (or [1,192]) NPY, as written by `spk-encoder-cli` and
// `zonos3-cli --save-speaker-embedding`. The vector is the raw encoder output (not normalized).
static inline bool mp_load_speaker_embedding(const std::string & path,
                                             std::vector<float> & v, std::string & err) {
    v.clear();
    FILE * f = fopen(path.c_str(), "rb");
    if (!f) { err = "cannot open speaker embedding: " + path; return false; }
    auto fail = [&](const char * why) { fclose(f); err = why; v.clear(); return false; };
    unsigned char prefix[8];
    if (fread(prefix, 1, 8, f) != 8 || memcmp(prefix, "\x93NUMPY", 6) ||
        prefix[7] != 0 || (prefix[6] != 1 && prefix[6] != 2 && prefix[6] != 3))
        return fail("invalid NPY speaker embedding header");
    unsigned char hl[4] = {};
    const int hbytes = prefix[6] == 1 ? 2 : 4;
    if (fread(hl, 1, hbytes, f) != (size_t) hbytes) return fail("truncated NPY header");
    const uint32_t nheader = hl[0] | ((uint32_t) hl[1] << 8) |
                             ((uint32_t) hl[2] << 16) | ((uint32_t) hl[3] << 24);
    if (nheader > 65536) return fail("oversized NPY header");
    std::string header(nheader, '\0');
    if (fread(header.data(), 1, nheader, f) != nheader) return fail("truncated NPY header");
    const size_t order = header.find("fortran_order");
    const size_t shape = header.find("shape");
    const size_t descr = header.find("descr");
    if (order == std::string::npos || shape == std::string::npos || descr == std::string::npos)
        return fail("invalid NPY speaker embedding metadata");
    size_t p = header.find(':', order);
    if (p == std::string::npos) return fail("invalid NPY array order");
    p = header.find_first_not_of(" \t", p + 1);
    if (p == std::string::npos || header.compare(p, 5, "False") != 0)
        return fail("speaker embedding NPY must be C-order");
    p = header.find(':', descr);
    if (p == std::string::npos) return fail("invalid NPY dtype");
    p = header.find_first_of("'\"", p + 1);
    if (p == std::string::npos) return fail("invalid NPY dtype");
    const size_t end = header.find(header[p], p + 1);
    if (end == std::string::npos) return fail("invalid NPY dtype");
    const std::string dtype = header.substr(p + 1, end - p - 1);
    if (dtype != "<f4" && dtype != "<f8") return fail("speaker embedding NPY needs little-endian f4 or f8 dtype");
    p = header.find('(', shape);
    const size_t close = p == std::string::npos ? p : header.find(')', p);
    if (close == std::string::npos) return fail("invalid NPY shape");
    std::vector<long long> dims;
    for (size_t i = p + 1; i < close;) {
        if (header[i] < '0' || header[i] > '9') { ++i; continue; }
        long long value = 0;
        while (i < close && header[i] >= '0' && header[i] <= '9' && value < 1000000) value = value * 10 + (header[i++] - '0');
        dims.push_back(value);
    }
    if (!(dims == std::vector<long long>{192} || dims == std::vector<long long>{1, 192}))
        return fail("speaker embedding must have shape [192] or [1,192]");
    const int bytes = dtype[2] - '0';
    v.resize(192);
    for (float & x : v) {
        unsigned char raw[8] = {};
        if (fread(raw, 1, bytes, f) != (size_t) bytes) return fail("truncated speaker embedding");
        double value;
        if (bytes == 4) { float t; memcpy(&t, raw, 4); value = t; }
        else memcpy(&value, raw, 8);
        if (!std::isfinite(value) || std::fabs(value) > (double) std::numeric_limits<float>::max())
            return fail("speaker embedding contains a non-finite value");
        x = (float) value;
    }
    if (fgetc(f) != EOF) return fail("unexpected trailing data in speaker embedding NPY");
    fclose(f);
    return true;
}

static inline bool mp_save_speaker_embedding(const std::string & path, const std::vector<float> & v) {
    if (v.size() != 192) return false;
    std::string header = "{'descr': '<f4', 'fortran_order': False, 'shape': (192,), }";
    header.append((64 - (10 + header.size() + 1) % 64) % 64, ' ');
    header += '\n';
    FILE * f = fopen(path.c_str(), "wb");
    if (!f) return false;
    const unsigned char prefix[10] = {0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0,
                                     (unsigned char) header.size(), (unsigned char) (header.size() >> 8)};
    bool ok = fwrite(prefix, 1, 10, f) == 10 && fwrite(header.data(), 1, header.size(), f) == header.size();
    for (float x : v) {
        uint32_t u; memcpy(&u, &x, 4);
        const unsigned char raw[4] = {(unsigned char) u, (unsigned char) (u >> 8),
                                      (unsigned char) (u >> 16), (unsigned char) (u >> 24)};
        ok = ok && fwrite(raw, 1, 4, f) == 4;
    }
    return fclose(f) == 0 && ok;
}
