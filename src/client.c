/* A terminal client only; collection belongs to the shared collector. */
#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

static int wake_pipe[2] = {-1, -1};
static volatile sig_atomic_t stopping = 0;
static volatile sig_atomic_t resizing = 0;
static const char restore[] = "\033[0m\033[?25h";

static void notice(int number) {
    int saved_errno = errno;
    unsigned char byte = 1;
    if (number == SIGWINCH) resizing = 1;
    else stopping = 1;
    if (wake_pipe[1] >= 0) {
        ssize_t ignored = write(wake_pipe[1], &byte, 1);
        (void)ignored;
    }
    errno = saved_errno;
}

static int valid_uuid(const char *value) {
    if (strlen(value) != 36) return 0;
    for (size_t i = 0; i < 36; ++i) {
        if (i == 8 || i == 13 || i == 18 || i == 23) {
            if (value[i] != '-') return 0;
        } else if (!((value[i] >= '0' && value[i] <= '9') ||
                     (value[i] >= 'a' && value[i] <= 'f') ||
                     (value[i] >= 'A' && value[i] <= 'F'))) return 0;
    }
    return 1;
}

static int nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL);
    if (flags < 0 || fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) < 0) return -1;
    return fcntl(descriptor, F_SETFD, FD_CLOEXEC);
}

static void drain_notices(void) {
    char bytes[128];
    while (read(wake_pipe[0], bytes, sizeof bytes) > 0) {}
}

static int wait_writable(int descriptor) {
    struct pollfd waiting[2] = {{descriptor, POLLOUT, 0}, {wake_pipe[0], POLLIN, 0}};
    while (!stopping) {
        int result = poll(waiting, 2, -1);
        if (result < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        if (waiting[1].revents & POLLIN) drain_notices();
        if (waiting[0].revents & (POLLERR | POLLHUP | POLLNVAL)) return -1;
        if (waiting[0].revents & POLLOUT) return 0;
    }
    return -1;
}

static int write_all(int descriptor, const char *data, size_t size) {
    while (size > 0 && !stopping) {
        ssize_t written = write(descriptor, data, size);
        if (written > 0) {
            data += written;
            size -= (size_t)written;
        } else if (written < 0 && errno == EINTR) {
            continue;
        } else if (written < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            if (wait_writable(descriptor) < 0) return -1;
        } else return -1;
    }
    return size == 0 ? 0 : -1;
}

static unsigned int terminal_width(void) {
    struct winsize size;
    unsigned int width = 80;
    memset(&size, 0, sizeof size);
    if ((ioctl(STDOUT_FILENO, TIOCGWINSZ, &size) == 0 ||
         ioctl(STDIN_FILENO, TIOCGWINSZ, &size) == 0) && size.ws_col > 0)
        width = size.ws_col;
    if (width > 1) --width; /* Reserve the terminal's wrap column. */
    return width > 500 ? 500 : width;
}

static int send_width(int socket_fd, const char *run_id) {
    char message[160];
    int length = run_id == NULL
        ? snprintf(message, sizeof message, "{\"width\":%u}\n", terminal_width())
        : snprintf(message, sizeof message,
                   "{\"v\":1,\"run\":\"%s\",\"width\":%u,\"format\":\"ansi\"}\n",
                   run_id, terminal_width());
    if (length < 0 || (size_t)length >= sizeof message) return -1;
    return write_all(socket_fd, message, (size_t)length);
}

int main(int argc, char **argv) {
    struct sockaddr_un address;
    struct sigaction handler;
    int socket_fd = -1;
    int result = 2;
    int terminal_changed = 0;
    if (argc != 3 || !valid_uuid(argv[2]) || strlen(argv[1]) >= sizeof address.sun_path) return 2;
    if (pipe(wake_pipe) < 0) return 2;
    if (nonblocking(wake_pipe[0]) < 0 || nonblocking(wake_pipe[1]) < 0) goto done;
    memset(&handler, 0, sizeof handler);
    sigemptyset(&handler.sa_mask);
    handler.sa_handler = notice;
    if (sigaction(SIGWINCH, &handler, NULL) < 0 || sigaction(SIGTERM, &handler, NULL) < 0 ||
        sigaction(SIGHUP, &handler, NULL) < 0 || sigaction(SIGINT, &handler, NULL) < 0) goto done;
    handler.sa_handler = SIG_IGN;
    if (sigaction(SIGPIPE, &handler, NULL) < 0) goto done;
    socket_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (socket_fd < 0 || nonblocking(socket_fd) < 0) goto done;
    memset(&address, 0, sizeof address);
    address.sun_family = AF_UNIX;
    memcpy(address.sun_path, argv[1], strlen(argv[1]) + 1);
    if (connect(socket_fd, (struct sockaddr *)&address, sizeof address) < 0) {
        if (errno != EINPROGRESS) goto done;
        struct pollfd connecting[2] = {{socket_fd, POLLOUT, 0}, {wake_pipe[0], POLLIN, 0}};
        int ready = poll(connecting, 2, 5000);
        int error = 0;
        socklen_t length = sizeof error;
        if (ready <= 0 || stopping || !(connecting[0].revents & POLLOUT) ||
            getsockopt(socket_fd, SOL_SOCKET, SO_ERROR, &error, &length) < 0 || error != 0) goto done;
    }
    if (send_width(socket_fd, argv[2]) < 0) goto done;
    const char prefix[] = "\033[?25l\033[2J";
    terminal_changed = 1;
    if (write_all(STDOUT_FILENO, prefix, sizeof prefix - 1) < 0) goto done;
    result = 0;
    while (!stopping) {
        if (resizing) {
            resizing = 0;
            if (send_width(socket_fd, NULL) < 0) break;
        }
        struct pollfd waiting[2] = {{socket_fd, POLLIN, 0}, {wake_pipe[0], POLLIN, 0}};
        int ready = poll(waiting, 2, -1);
        if (ready < 0) {
            if (errno == EINTR) continue;
            result = 2;
            break;
        }
        if (waiting[1].revents & POLLIN) drain_notices();
        if (stopping) break;
        if (waiting[0].revents & (POLLIN | POLLHUP)) {
            char buffer[16384];
            ssize_t received = read(socket_fd, buffer, sizeof buffer);
            if (received == 0) break;
            if (received > 0) {
                if (write_all(STDOUT_FILENO, buffer, (size_t)received) < 0) break;
            } else if (errno != EINTR && errno != EAGAIN && errno != EWOULDBLOCK) {
                result = 2;
                break;
            }
        }
        if (waiting[0].revents & (POLLERR | POLLNVAL)) break;
    }
done:
    if (terminal_changed) {
        ssize_t ignored = write(STDOUT_FILENO, restore, sizeof restore - 1);
        (void)ignored;
    }
    if (socket_fd >= 0) close(socket_fd);
    close(wake_pipe[0]);
    close(wake_pipe[1]);
    return stopping ? 0 : result;
}
