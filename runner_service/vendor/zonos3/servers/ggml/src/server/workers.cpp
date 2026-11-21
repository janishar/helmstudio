#include "server/workers.h"

#include <cerrno>
#include <cstring>

#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#else
#include <fcntl.h>
#include <signal.h>
#include <spawn.h>
#include <sys/wait.h>
#include <unistd.h>
extern char ** environ;
#endif

namespace zs {

namespace {

// The server environment with the overrides applied, as NAME=VALUE strings.
std::vector<std::string> child_environment(const std::map<std::string, std::optional<std::string>> & env) {
    std::map<std::string, std::string> vars;
#ifdef _WIN32
    char * block = GetEnvironmentStringsA();
    for (const char * p = block; p && *p; p += strlen(p) + 1) {
        const char * eq = strchr(p + 1, '='); // entries like "=C:=C:\\" start with '='
        if (eq) vars[std::string(p, eq - p)] = eq + 1;
    }
    if (block) FreeEnvironmentStringsA(block);
#else
    for (char ** e = environ; e && *e; ++e) {
        const char * eq = strchr(*e, '=');
        if (eq) vars[std::string(*e, eq - *e)] = eq + 1;
    }
#endif
    for (const auto & [k, v] : env) {
        if (v) vars[k] = *v;
        else vars.erase(k);
    }
    std::vector<std::string> out;
    for (const auto & [k, v] : vars) out.push_back(k + "=" + v);
    return out;
}

} // namespace

#ifdef _WIN32

static std::string quote_arg(const std::string & a) {
    if (!a.empty() && a.find_first_of(" \t\"") == std::string::npos) return a;
    std::string q = "\"";
    size_t backslashes = 0;
    for (char c : a) {
        if (c == '\\') {
            ++backslashes;
            continue;
        }
        if (c == '"') q.append(backslashes * 2 + 1, '\\');
        else q.append(backslashes, '\\');
        backslashes = 0;
        q += c;
    }
    q.append(backslashes * 2, '\\');
    return q + "\"";
}

ChildProcess::ChildProcess(const std::vector<std::string> & argv,
                           const std::map<std::string, std::optional<std::string>> & env) {
    SECURITY_ATTRIBUTES sa{sizeof(sa), nullptr, TRUE};
    HANDLE in_r, in_w, out_r, out_w;
    if (!CreatePipe(&in_r, &in_w, &sa, 0)) throw WorkerError("cannot create the worker's stdin pipe");
    if (!CreatePipe(&out_r, &out_w, &sa, 0)) {
        CloseHandle(in_r);
        CloseHandle(in_w);
        throw WorkerError("cannot create the worker's stdout pipe");
    }
    SetHandleInformation(in_w, HANDLE_FLAG_INHERIT, 0);
    SetHandleInformation(out_r, HANDLE_FLAG_INHERIT, 0);
    std::string cmd;
    for (const auto & a : argv) cmd += (cmd.empty() ? "" : " ") + quote_arg(a);
    std::string envblock;
    for (const auto & e : child_environment(env)) {
        envblock += e;
        envblock += '\0';
    }
    envblock += '\0';
    STARTUPINFOA si{};
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdInput = in_r;
    si.hStdOutput = out_w;
    si.hStdError = GetStdHandle(STD_ERROR_HANDLE);
    PROCESS_INFORMATION pi{};
    const BOOL ok = CreateProcessA(nullptr, cmd.data(), nullptr, nullptr, TRUE, CREATE_NO_WINDOW, envblock.data(),
                                   nullptr, &si, &pi);
    CloseHandle(in_r);
    CloseHandle(out_w);
    if (!ok) {
        CloseHandle(in_w);
        CloseHandle(out_r);
        throw WorkerError("cannot start " + argv[0]);
    }
    CloseHandle(pi.hThread);
    process_ = pi.hProcess;
    in_ = in_w;
    out_ = out_r;
}

bool ChildProcess::write(const std::string & data) {
    if (!in_) return false;
    DWORD done = 0;
    for (size_t off = 0; off < data.size(); off += done)
        if (!WriteFile((HANDLE) in_, data.data() + off, (DWORD) (data.size() - off), &done, nullptr)) return false;
    return true;
}

bool ChildProcess::read_line(std::string & line) {
    for (;;) {
        const size_t nl = pending_.find('\n');
        if (nl != std::string::npos) {
            line = pending_.substr(0, nl);
            pending_.erase(0, nl + 1);
            return true;
        }
        char buf[65536];
        DWORD n = 0;
        if (!out_ || !ReadFile((HANDLE) out_, buf, sizeof(buf), &n, nullptr) || n == 0) return false;
        pending_.append(buf, n);
    }
}

void ChildProcess::close_input() {
    if (in_) CloseHandle((HANDLE) in_);
    in_ = nullptr;
}

void ChildProcess::wait() {
    close_input();
    if (process_) WaitForSingleObject((HANDLE) process_, INFINITE);
    if (out_) CloseHandle((HANDLE) out_);
    if (process_) CloseHandle((HANDLE) process_);
    process_ = out_ = nullptr;
}

#else

ChildProcess::ChildProcess(const std::vector<std::string> & argv,
                           const std::map<std::string, std::optional<std::string>> & env) {
    int in_pipe[2], out_pipe[2];
    if (pipe(in_pipe) != 0) throw WorkerError(std::string("cannot create the worker's stdin pipe: ") + strerror(errno));
    if (pipe(out_pipe) != 0) {
        ::close(in_pipe[0]);
        ::close(in_pipe[1]);
        throw WorkerError(std::string("cannot create the worker's stdout pipe: ") + strerror(errno));
    }
    fcntl(in_pipe[1], F_SETFD, FD_CLOEXEC);
    fcntl(out_pipe[0], F_SETFD, FD_CLOEXEC);
    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    posix_spawn_file_actions_adddup2(&fa, in_pipe[0], 0);
    posix_spawn_file_actions_adddup2(&fa, out_pipe[1], 1);
    posix_spawn_file_actions_addclose(&fa, in_pipe[0]);
    posix_spawn_file_actions_addclose(&fa, out_pipe[1]);
    std::vector<std::string> args_copy = argv;
    std::vector<char *> args;
    for (auto & a : args_copy) args.push_back(a.data());
    args.push_back(nullptr);
    std::vector<std::string> envs = child_environment(env);
    std::vector<char *> envp;
    for (auto & e : envs) envp.push_back(e.data());
    envp.push_back(nullptr);
    pid_t pid = -1;
    const int rc = posix_spawnp(&pid, args[0], &fa, nullptr, args.data(), envp.data());
    posix_spawn_file_actions_destroy(&fa);
    ::close(in_pipe[0]);
    ::close(out_pipe[1]);
    if (rc != 0) {
        ::close(in_pipe[1]);
        ::close(out_pipe[0]);
        throw WorkerError("cannot start " + argv[0] + ": " + strerror(rc));
    }
    pid_ = pid;
    in_ = in_pipe[1];
    out_ = out_pipe[0];
}

bool ChildProcess::write(const std::string & data) {
    if (in_ < 0) return false;
    for (size_t off = 0; off < data.size();) {
        const ssize_t n = ::write(in_, data.data() + off, data.size() - off);
        if (n < 0) {
            if (errno == EINTR) continue;
            return false;
        }
        off += (size_t) n;
    }
    return true;
}

bool ChildProcess::read_line(std::string & line) {
    for (;;) {
        const size_t nl = pending_.find('\n');
        if (nl != std::string::npos) {
            line = pending_.substr(0, nl);
            pending_.erase(0, nl + 1);
            return true;
        }
        if (out_ < 0) return false;
        char buf[65536];
        const ssize_t n = ::read(out_, buf, sizeof(buf));
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) return false;
        pending_.append(buf, (size_t) n);
    }
}

void ChildProcess::close_input() {
    if (in_ >= 0) ::close(in_);
    in_ = -1;
}

void ChildProcess::wait() {
    close_input();
    if (pid_ >= 0) {
        int status = 0;
        while (waitpid(pid_, &status, 0) < 0 && errno == EINTR) {}
        pid_ = -1;
    }
    if (out_ >= 0) ::close(out_);
    out_ = -1;
}

#endif

ChildProcess::~ChildProcess() {
    wait();
}

int run_process(const std::vector<std::string> & argv, std::string & stderr_text) {
    return run_process(argv, {}, stderr_text);
}

#ifdef _WIN32
int run_process(const std::vector<std::string> & argv, const std::map<std::string, std::optional<std::string>> & env,
                std::string & stderr_text) {
    stderr_text.clear();
    std::string envblock;
    for (const auto & e : child_environment(env)) {
        envblock += e;
        envblock += '\0';
    }
    envblock += '\0';
    SECURITY_ATTRIBUTES sa{sizeof(sa), nullptr, TRUE};
    HANDLE err_r, err_w;
    if (!CreatePipe(&err_r, &err_w, &sa, 0)) {
        stderr_text = "cannot create a pipe";
        return -1;
    }
    SetHandleInformation(err_r, HANDLE_FLAG_INHERIT, 0);
    HANDLE nul = CreateFileA("NUL", GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, nullptr);
    std::string cmd;
    for (const auto & a : argv) cmd += (cmd.empty() ? "" : " ") + quote_arg(a);
    STARTUPINFOA si{};
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdInput = nul;
    si.hStdOutput = nul;
    si.hStdError = err_w;
    PROCESS_INFORMATION pi{};
    const BOOL ok = CreateProcessA(nullptr, cmd.data(), nullptr, nullptr, TRUE, CREATE_NO_WINDOW, envblock.data(),
                                   nullptr, &si, &pi);
    CloseHandle(err_w);
    if (nul != INVALID_HANDLE_VALUE) CloseHandle(nul);
    if (!ok) {
        CloseHandle(err_r);
        stderr_text = "cannot start " + argv[0];
        return -1;
    }
    char buf[4096];
    DWORD n = 0;
    while (ReadFile(err_r, buf, sizeof(buf), &n, nullptr) && n > 0) stderr_text.append(buf, n);
    CloseHandle(err_r);
    WaitForSingleObject(pi.hProcess, INFINITE);
    DWORD code = 1;
    GetExitCodeProcess(pi.hProcess, &code);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return (int) code;
}
#else
int run_process(const std::vector<std::string> & argv, const std::map<std::string, std::optional<std::string>> & env,
                std::string & stderr_text) {
    stderr_text.clear();
    std::vector<std::string> envs = child_environment(env);
    std::vector<char *> envp;
    for (auto & e : envs) envp.push_back(e.data());
    envp.push_back(nullptr);
    int err_pipe[2];
    if (pipe(err_pipe) != 0) {
        stderr_text = "cannot create a pipe";
        return -1;
    }
    fcntl(err_pipe[0], F_SETFD, FD_CLOEXEC);
    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    posix_spawn_file_actions_addopen(&fa, 0, "/dev/null", O_RDONLY, 0);
    posix_spawn_file_actions_addopen(&fa, 1, "/dev/null", O_WRONLY, 0);
    posix_spawn_file_actions_adddup2(&fa, err_pipe[1], 2);
    posix_spawn_file_actions_addclose(&fa, err_pipe[1]);
    std::vector<std::string> args_copy = argv;
    std::vector<char *> args;
    for (auto & a : args_copy) args.push_back(a.data());
    args.push_back(nullptr);
    pid_t pid = -1;
    const int rc = posix_spawnp(&pid, args[0], &fa, nullptr, args.data(), envp.data());
    posix_spawn_file_actions_destroy(&fa);
    ::close(err_pipe[1]);
    if (rc != 0) {
        ::close(err_pipe[0]);
        stderr_text = "cannot start " + argv[0] + ": " + strerror(rc);
        return -1;
    }
    char buf[4096];
    for (;;) {
        const ssize_t n = ::read(err_pipe[0], buf, sizeof(buf));
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) break;
        stderr_text.append(buf, (size_t) n);
    }
    ::close(err_pipe[0]);
    int status = 0;
    while (waitpid(pid, &status, 0) < 0 && errno == EINTR) {}
    return WIFEXITED(status) ? WEXITSTATUS(status) : -1;
}
#endif

} // namespace zs
