// In-process embedding hooks for the zonos3 HTTP server (used by zonos3-app).
//
// zonos3_server_main is the whole of zonos3-server's main(): it loads the models,
// spawns the worker/ZAC threads, binds, and blocks serving requests until
// httplib::Server::stop() is called (or bind fails). A non-null `ctl` lets an
// embedder observe startup and shut the server down:
//
//   zonos3_server_ctl ctl;
//   std::thread th([&] { zonos3_server_main(argc, argv, &ctl); });
//   ...wait for ctl.port > 0 (listening) or ctl.failed...
//   ctl.svr->stop();   // from any thread; zonos3_server_main then returns
//   th.join();
#pragma once

#include <atomic>

namespace httplib { class Server; }

struct zonos3_server_ctl {
    std::atomic<int>  port{0};      // actual bound port once listening (--port 0 = ephemeral)
    std::atomic<bool> failed{false};// set on any startup failure (bad args, model load, bind)
    std::atomic<httplib::Server *> svr{nullptr};   // set just before the accept loop starts,
                                    // cleared before zonos3_server_main returns
};

int zonos3_server_main(int argc, char ** argv, zonos3_server_ctl * ctl);
