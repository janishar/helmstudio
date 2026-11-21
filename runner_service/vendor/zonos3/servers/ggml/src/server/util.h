// Small shared helpers of the HTTP server: base64, SHA-1 / SHA-256, files, time, ids.
#pragma once

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <string>
#include <vector>

#include "zonos3-text.h"

namespace zs {

// Python's base64.b64decode(payload, validate=True) after stripping and an optional "data:...," prefix
// (zonos3.reference.synth.decode_base64_blob). Returns false on an invalid payload.
bool b64_decode_strict(const std::string & in, std::vector<uint8_t> & out);
std::string b64_encode(const uint8_t * data, size_t n);

// Incremental SHA-256 (FIPS 180-4).
class Sha256 {
  public:
    Sha256();
    void update(const void * data, size_t n);
    std::string hex(); // finalizes
  private:
    void block(const uint8_t * p);
    uint32_t h_[8];
    uint8_t buf_[64];
    size_t len_ = 0;
    uint64_t total_ = 0;
};
std::string sha1_hex(const std::string & data);

bool read_file(const std::filesystem::path & path, std::string & out);
double unix_now();                                  // seconds, sub-second precision
double file_mtime(const std::filesystem::path & p); // seconds (sub-second where available), 0 on error
std::string random_hex(size_t n_chars);             // lowercase hex from a CSPRNG-seeded generator (uuid4().hex[:n])

std::string lower_ascii(std::string s);
std::string strip(const std::string & s); // Python str.strip(): Unicode whitespace on both ends

// /v2/controls and `controls` without a loaded steering bundle (each site appends its own suffix).
constexpr const char * NO_STEERING =
    "This server has no steering bundle (place steer.gguf next to the backbone or pass --steer)";

// Python repr() of a str for error messages ('...' with escapes for quotes and backslashes).
std::string py_repr(const std::string & s);

// A float32 metadata value as the shortest decimal that round-trips it (the checkpoint's original value, e.g. -29.002
// rather than -29.00200080871582), as a double.
double float_decimal(float v);

} // namespace zs
