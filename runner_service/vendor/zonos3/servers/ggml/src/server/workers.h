// Helper processes of the server (model/src/zonos3/workers/*.py): ChildProcess, a child with its stdin and stdout on
// pipes. The Python host (server/pyhost.h) multiplexes its own ChildProcess.
#pragma once

#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

namespace zs {

struct WorkerError : std::runtime_error {
    using std::runtime_error::runtime_error;
};

// A started child process: stdin and stdout are pipes, stderr is the server's. write() and read_line() may run on
// different threads; every other call (and two concurrent writes) must be serialized by the owner.
class ChildProcess {
  public:
    // argv[0] is the program (searched on PATH when it has no slash). `env` overrides the server's environment: a
    // value sets the variable, nullopt removes it. Throws WorkerError when the process cannot start.
    ChildProcess(const std::vector<std::string> & argv, const std::map<std::string, std::optional<std::string>> & env);
    ~ChildProcess(); // wait()
    ChildProcess(const ChildProcess &) = delete;
    ChildProcess & operator=(const ChildProcess &) = delete;

    // All of `data` to the child's stdin; false once stdin is closed or the child stopped reading.
    bool write(const std::string & data);
    // The next stdout line without its '\n'; false at end of stream.
    bool read_line(std::string & line);
    // Close the child's stdin (it sees end of input).
    void close_input();
    // Close stdin, wait for the child to exit and close stdout.
    void wait();

  private:
    std::string pending_; // bytes read past the last line
#ifdef _WIN32
    void *process_ = nullptr, *in_ = nullptr, *out_ = nullptr;
#else
    int pid_ = -1, in_ = -1, out_ = -1;
#endif
};

// Runs argv[0] (searched on PATH) with argv, stdin and stdout on the null device, and waits for it. Returns the exit
// code (-1 when it could not start or did not exit normally) with the child's stderr in `stderr_text`.
int run_process(const std::vector<std::string> & argv, std::string & stderr_text);
// The same with the server environment plus `env` (a nullopt value removes the variable), as ChildProcess.
int run_process(const std::vector<std::string> & argv, const std::map<std::string, std::optional<std::string>> & env,
                std::string & stderr_text);

} // namespace zs
