#ifdef _WIN32
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0600
#endif
#endif
#include "compat.h"
#include "audio-decode.h"
#include "exe-path.h"
#include "npy.h"
#include "zonos3-text.h"
#include <algorithm>
#include <array>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cwctype>
#include <filesystem>
#include <string>
#include <system_error>
#include <vector>
static std::string shell_quote_path(const char * path) {
    std::string q = "'";
    for (const char * p = path; *p; ++p) q += (*p == '\'') ? "'\\''" : std::string(1, *p);
    return q + "'";
}

// Locate the ffmpeg used to decode reference audio for cloning. The launcher and the
// desktop app download an ffmpeg on demand and point us at it via ZONOS3_FFMPEG; we
// also look next to the executable (dist layout / macOS bundle Resources) before
// falling back to a bare "ffmpeg" on PATH.
std::filesystem::path audio_ffmpeg_path() {
    namespace fs = std::filesystem;
#ifdef _WIN32
    if (const wchar_t * e = _wgetenv(L"ZONOS3_FFMPEG"); e && *e) return fs::path(e);
    const fs::path name = L"ffmpeg.exe";
#else
    if (const char * e = getenv("ZONOS3_FFMPEG"); e && *e) return fs::path(e);
    const fs::path name = "ffmpeg";
#endif
    std::error_code ec;
    const fs::path dir = zonos3_exe_dir();
    for (const fs::path c : {dir / name, dir / "bin" / name, dir / ".." / "Resources" / name})
        if (fs::exists(c, ec)) return c.lexically_normal();
    return name; // rely on PATH
}

#ifdef _WIN32
namespace {

// A small owning wrapper keeps every process/pipe handle on one cleanup path.
class win_handle {
  public:
    win_handle() = default;
    explicit win_handle(HANDLE handle) : handle_(handle) {}
    ~win_handle() { reset(); }

    win_handle(const win_handle &) = delete;
    win_handle & operator=(const win_handle &) = delete;

    HANDLE get() const { return handle_; }
    bool valid() const { return handle_ != nullptr && handle_ != INVALID_HANDLE_VALUE; }

    void reset(HANDLE handle = INVALID_HANDLE_VALUE) {
        if (valid()) CloseHandle(handle_);
        handle_ = handle;
    }

  private:
    HANDLE handle_ = INVALID_HANDLE_VALUE;
};

static void log_windows_error(const char * what, DWORD error) {
    fprintf(stderr, "audio: %s (Windows error %lu)\n", what, (unsigned long) error);
}

// CreateProcessW does not search PATH when lpApplicationName is supplied as a bare
// filename. Resolve that case ourselves, while keeping explicit paths explicit.
static std::filesystem::path resolve_windows_executable(const std::filesystem::path & requested) {
    namespace fs = std::filesystem;
    if (requested.has_parent_path()) {
        std::error_code ec;
        const fs::path absolute = fs::absolute(requested, ec);
        return ec ? requested : absolute.lexically_normal();
    }

    std::vector<wchar_t> buffer(32768);
    const DWORD n = SearchPathW(
        nullptr,
        requested.c_str(),
        nullptr,
        (DWORD) buffer.size(),
        buffer.data(),
        nullptr);
    if (n == 0 || n >= buffer.size()) return {};
    return fs::path(buffer.data(), buffer.data() + n);
}

// Quote one argv element according to the parsing rules used by the Microsoft C
// runtime. This is argument quoting only; no command shell is involved.
static std::wstring quote_windows_argument(const std::wstring & argument) {
    const bool needs_quotes = argument.empty() || std::any_of(
                                                      argument.begin(),
                                                      argument.end(),
                                                      [](wchar_t c) { return std::iswspace(c) || c == L'"'; });
    if (!needs_quotes) return argument;

    std::wstring quoted(1, L'"');
    size_t backslashes = 0;
    for (const wchar_t c : argument) {
        if (c == L'\\') {
            ++backslashes;
        } else if (c == L'"') {
            quoted.append(backslashes * 2 + 1, L'\\');
            quoted.push_back(L'"');
            backslashes = 0;
        } else {
            quoted.append(backslashes, L'\\');
            backslashes = 0;
            quoted.push_back(c);
        }
    }
    quoted.append(backslashes * 2, L'\\');
    quoted.push_back(L'"');
    return quoted;
}

static win_handle inheritable_stderr(SECURITY_ATTRIBUTES & security) {
    HANDLE duplicate = INVALID_HANDLE_VALUE;
    const HANDLE current = GetStdHandle(STD_ERROR_HANDLE);
    if (current != nullptr && current != INVALID_HANDLE_VALUE &&
        DuplicateHandle(
            GetCurrentProcess(),
            current,
            GetCurrentProcess(),
            &duplicate,
            0,
            TRUE,
            DUPLICATE_SAME_ACCESS)) {
        return win_handle(duplicate);
    }

    return win_handle(CreateFileW(
        L"NUL",
        GENERIC_WRITE,
        FILE_SHARE_READ | FILE_SHARE_WRITE,
        &security,
        OPEN_EXISTING,
        FILE_ATTRIBUTE_NORMAL,
        nullptr));
}

static std::vector<float> decode_audio_windows(const char * path, int sample_rate, int max_seconds) {
    namespace fs = std::filesystem;

    const fs::path ffmpeg = resolve_windows_executable(audio_ffmpeg_path());
    if (ffmpeg.empty()) {
        fprintf(stderr, "audio: cannot find ffmpeg.exe\n");
        return {};
    }

    const fs::path input = fs::u8path(path);
    std::wstring command =
        quote_windows_argument(ffmpeg.native()) +
        L" -nostdin -v error -i " +
        quote_windows_argument(input.native()) +
        L" -ac 1 -ar " +
        std::to_wstring(sample_rate) +
        L" -t " + std::to_wstring(max_seconds) + L" -f f32le -";

    SECURITY_ATTRIBUTES security{};
    security.nLength = sizeof(security);
    security.bInheritHandle = TRUE;

    HANDLE stdout_read_raw = INVALID_HANDLE_VALUE;
    HANDLE stdout_write_raw = INVALID_HANDLE_VALUE;
    if (!CreatePipe(&stdout_read_raw, &stdout_write_raw, &security, 0)) {
        log_windows_error("cannot create ffmpeg output pipe", GetLastError());
        return {};
    }
    win_handle stdout_read(stdout_read_raw);
    win_handle stdout_write(stdout_write_raw);
    if (!SetHandleInformation(stdout_read.get(), HANDLE_FLAG_INHERIT, 0)) {
        log_windows_error("cannot protect ffmpeg output pipe", GetLastError());
        return {};
    }

    win_handle stdin_null(CreateFileW(
        L"NUL",
        GENERIC_READ,
        FILE_SHARE_READ | FILE_SHARE_WRITE,
        &security,
        OPEN_EXISTING,
        FILE_ATTRIBUTE_NORMAL,
        nullptr));
    win_handle stderr_handle = inheritable_stderr(security);
    if (!stdin_null.valid() || !stderr_handle.valid()) {
        log_windows_error("cannot prepare ffmpeg standard handles", GetLastError());
        return {};
    }

    std::array<HANDLE, 3> inherited_handles = {
        stdin_null.get(),
        stdout_write.get(),
        stderr_handle.get(),
    };
    SIZE_T attribute_bytes = 0;
    InitializeProcThreadAttributeList(nullptr, 1, 0, &attribute_bytes);
    if (attribute_bytes == 0) {
        log_windows_error("cannot size ffmpeg process attributes", GetLastError());
        return {};
    }

    std::vector<unsigned char> attribute_storage(attribute_bytes);
    auto * attributes =
        reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(attribute_storage.data());
    if (!InitializeProcThreadAttributeList(attributes, 1, 0, &attribute_bytes)) {
        log_windows_error("cannot initialize ffmpeg process attributes", GetLastError());
        return {};
    }

    const bool handles_updated = UpdateProcThreadAttribute(
                                     attributes,
                                     0,
                                     PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
                                     inherited_handles.data(),
                                     sizeof(inherited_handles),
                                     nullptr,
                                     nullptr) != FALSE;
    if (!handles_updated) {
        const DWORD error = GetLastError();
        DeleteProcThreadAttributeList(attributes);
        log_windows_error("cannot restrict ffmpeg inherited handles", error);
        return {};
    }

    STARTUPINFOEXW startup{};
    startup.StartupInfo.cb = sizeof(startup);
    startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
    startup.StartupInfo.hStdInput = stdin_null.get();
    startup.StartupInfo.hStdOutput = stdout_write.get();
    startup.StartupInfo.hStdError = stderr_handle.get();
    startup.lpAttributeList = attributes;

    PROCESS_INFORMATION process_info{};
    const BOOL created = CreateProcessW(
        ffmpeg.c_str(),
        command.data(),
        nullptr,
        nullptr,
        TRUE,
        EXTENDED_STARTUPINFO_PRESENT | CREATE_NO_WINDOW,
        nullptr,
        nullptr,
        &startup.StartupInfo,
        &process_info);
    const DWORD create_error = created ? ERROR_SUCCESS : GetLastError();
    DeleteProcThreadAttributeList(attributes);
    if (!created) {
        log_windows_error("cannot start ffmpeg", create_error);
        return {};
    }

    win_handle process(process_info.hProcess);
    win_handle thread(process_info.hThread);
    // Only the child may retain these ends while the parent drains stdout.
    stdout_write.reset();
    stdin_null.reset();
    stderr_handle.reset();

    std::vector<unsigned char> raw;
    std::array<unsigned char, 32768> buffer{};
    DWORD read_error = ERROR_SUCCESS;
    for (;;) {
        DWORD bytes_read = 0;
        if (!ReadFile(
                stdout_read.get(),
                buffer.data(),
                (DWORD) buffer.size(),
                &bytes_read,
                nullptr)) {
            const DWORD error = GetLastError();
            if (error != ERROR_BROKEN_PIPE) read_error = error;
            break;
        }
        if (bytes_read == 0) break;
        raw.insert(raw.end(), buffer.begin(), buffer.begin() + bytes_read);
    }

    // If the pipe itself failed, close our read end before waiting so a child
    // that is still writing receives a broken pipe instead of blocking forever.
    if (read_error != ERROR_SUCCESS) stdout_read.reset();
    const DWORD wait_result = WaitForSingleObject(process.get(), INFINITE);
    DWORD exit_code = 1;
    const bool got_exit_code = GetExitCodeProcess(process.get(), &exit_code) != FALSE;
    if (read_error != ERROR_SUCCESS) {
        log_windows_error("cannot read ffmpeg output", read_error);
        return {};
    }
    if (wait_result != WAIT_OBJECT_0) {
        log_windows_error("cannot wait for ffmpeg", GetLastError());
        return {};
    }
    if (!got_exit_code) {
        log_windows_error("cannot read ffmpeg exit code", GetLastError());
        return {};
    }
    if (exit_code != 0 || raw.empty() || raw.size() % sizeof(float) != 0) {
        fprintf(
            stderr,
            "audio: ffmpeg failed (rc=%lu, %zu bytes) for %s\n",
            (unsigned long) exit_code,
            raw.size(),
            path);
        return {};
    }

    std::vector<float> wav(raw.size() / sizeof(float));
    memcpy(wav.data(), raw.data(), raw.size());
    return wav;
}

} // namespace
#endif

std::vector<float> audio_decode_file(const char * path, int sample_rate, int max_seconds) {
    if (!path || !*path || sample_rate <= 0 || max_seconds <= 0) return {};
#ifdef _WIN32
    // Do not use _popen here: it delegates to cmd.exe, whose /c quote stripping
    // breaks an absolute executable path followed by another quoted input path.
    return decode_audio_windows(path, sample_rate, max_seconds);
#else
    const std::string ffmpeg = audio_ffmpeg_path().string();
    std::string cmd = shell_quote_path(ffmpeg.c_str()) + " -nostdin -v error -i " +
                      shell_quote_path(path) + " -ac 1 -ar " + std::to_string(sample_rate) +
                      " -t " + std::to_string(max_seconds) + " -f f32le -";
    FILE * pp = popen(cmd.c_str(), "r");
    if (!pp) {
        fprintf(stderr, "audio: cannot run ffmpeg\n");
        return {};
    }
    std::vector<float> wav;
    float buf[8192];
    size_t n;
    while ((n = fread(buf, sizeof(float), 8192, pp)) > 0) wav.insert(wav.end(), buf, buf + n);
    int rc = pclose(pp);
    if (rc != 0 || wav.empty()) {
        fprintf(stderr, "audio: ffmpeg failed (rc=%d, %zu samples) for %s\n", rc, wav.size(), path);
        return {};
    }
    return wav;
#endif
}

bool audio_load_reference(const std::string & path, int max_seconds, std::vector<float> & out, std::string & err) {
    out.clear();
    if (ends_with(path, ".npy")) {
        std::vector<int64_t> shape;
        if (!npy::load_f32(path, out, shape) || !(shape.size() == 1 || (shape.size() == 2 && shape[0] == 1))) {
            out.clear();
            err = "PCM NPY must be F32 [samples] or [1,samples] at 44100 Hz";
            return false;
        }
    } else {
        out = audio_decode_file(path.c_str(), 44100, max_seconds);
        if (out.empty()) {
            err = "cannot decode reference audio";
            return false;
        }
    }
    if (out.empty() || out.size() > (size_t) max_seconds * 44100) {
        out.clear();
        err = "reference must contain 1.." + std::to_string((int64_t) max_seconds * 44100) + " mono samples";
        return false;
    }
    return true;
}
