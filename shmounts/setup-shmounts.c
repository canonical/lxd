#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/limits.h>
#include <mntent.h>
#include <sched.h>
#include <stdio.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

static ssize_t lxc_write_nointr(int fd, const void *buf, size_t count)
{
	ssize_t ret;
again:
	ret = write(fd, buf, count);
	if (ret < 0 && errno == EINTR)
		goto again;

	return ret;
}

static ssize_t lxc_read_nointr(int fd, void *buf, size_t count)
{
	ssize_t ret;
again:
	ret = read(fd, buf, count);
	if (ret < 0 && errno == EINTR)
		goto again;

	return ret;
}


int mkdir_p(const char *dir, mode_t mode)
{
	const char *tmp = dir;
	const char *orig = dir;

	do {
		int ret;
		char *makeme;

		dir = tmp + strspn(tmp, "/");
		tmp = dir + strcspn(dir, "/");

		errno = ENOMEM;
		makeme = strndup(orig, dir - orig);
		if (!makeme)
			return -1;

		ret = mkdir(makeme, mode);
		if (ret < 0 && errno != EEXIST) {
			free(makeme);
			return -1;
		}

		free(makeme);
	} while (tmp != dir);

	return 0;
}

__attribute__((noreturn))
static void die(const char *s)  {
	perror(s);
	exit(1);
}

int setup_ns() {
	int wstatus;
	ssize_t ret;
	int pipe_fds[2];
	char nspath[PATH_MAX];
	int nsfd_host = -1, fd = -1, pid = -1;

	// Create sync pipe
	ret = pipe2(pipe_fds, O_CLOEXEC);
	if (ret < 0) {
		return -1;
	}

	// Spawn child
	pid = fork();
	if (pid < 0) {
		close(pipe_fds[0]);
		close(pipe_fds[1]);
		return -1;
	}

	if (pid == 0) {
		close(pipe_fds[1]);

		// Wait for unshare to be done
		ret = lxc_read_nointr(pipe_fds[0], nspath, 1);
		close(pipe_fds[0]);
		if (ret < 0) {
			die("cannot read from pipe");
		}

		// Create the mountpoint
		if (mkdir("/var/snap/lxd/common/ns", 0700) < 0 && errno != EEXIST) {
			die("cannot mkdir /var/snap/lxd/common/ns");
		}

		// Mount a tmpfs
		if (mount("tmpfs", "/var/snap/lxd/common/ns", "tmpfs", 0, "size=1M,mode=0700") < 0) {
			die("cannot mount tmpfs on /var/snap/lxd/common/ns");
		}

		// Mark the tmpfs mount as MS_PRIVATE
		if (mount("none", "/var/snap/lxd/common/ns", NULL, MS_REC|MS_PRIVATE, NULL) < 0) {
			die("cannot change propagation on /var/snap/lxd/common/ns");
		}

		// Store reference to the mntns
		if (snprintf(nspath, PATH_MAX, "/proc/%u/ns/mnt", (unsigned)getppid()) < 0) {
			die("cannot capture reference to parent's mount ns");
		}

		fd = open("/var/snap/lxd/common/ns/shmounts", O_CREAT | O_RDWR, 0600);
		if (fd < 0) {
			die("cannot open /var/snap/lxd/common/ns/shmounts");
		}
		close(fd);

		if (mount(nspath, "/var/snap/lxd/common/ns/shmounts", NULL, MS_BIND, NULL) < 0) {
			die("cannot bind mount ns to /var/snap/lxd/common/ns/shmounts");
		}

		/* the child is done */
		exit(0);
	}

	close(pipe_fds[0]);

	// Unshare the mount namespace
	if (unshare(CLONE_NEWNS) < 0) {
		close(pipe_fds[1]);
		return -1;
	}

	ret = lxc_write_nointr(pipe_fds[1], "1", 1);
	close(pipe_fds[1]);
	if (ret < 0) {
		return -1;
	}

	// Create the mountpoint
	if (mkdir("/var/snap/lxd/common/shmounts", 0711) < 0 && errno != EEXIST) {
		return -1;
	}

	// Create a mount entry
	if (mount("/var/snap/lxd/common/shmounts", "/var/snap/lxd/common/shmounts", NULL, MS_BIND, NULL) < 0) {
		return -1;
	}

	// Mark the mount entry as MS_PRIVATE (hide from PID1)
	if (mount("none", "/var/snap/lxd/common/shmounts", NULL, MS_REC|MS_PRIVATE, NULL) < 0) {
		return -1;
	}

	// Mount a tmpfs
	if (mount("tmpfs", "/var/snap/lxd/common/shmounts", "tmpfs", 0, "size=1M,mode=0711") < 0) {
		return -1;
	}

	// Mark the tmpfs mount as MS_SHARED
	if (mount("none", "/var/snap/lxd/common/shmounts", NULL, MS_REC|MS_SHARED, NULL) < 0) {
		return -1;
	}

	// Wait for the child to be done
	if (wait(&wstatus) < 0) {
		return -1;
	}

	if (!WIFEXITED(wstatus) || WEXITSTATUS(wstatus) != 0) {
		return -1;
	}

	// Re-attach to PID1 mntns
	nsfd_host = open("/proc/1/ns/mnt", O_RDONLY);
	if (nsfd_host < 0) {
		return -1;
	}

	if (setns(nsfd_host, CLONE_NEWNS) < 0) {
		return -1;
	}
	close(nsfd_host);

	// Cleanup spare mount entry
	if (umount("/var/snap/lxd/common/shmounts") < 0) {
		return -1;
	}

	return 0;
}

int main() {
	bool setup = true;
	bool run_media = false;
	int fd = -1;
	int nsfd_current = -1, nsfd_old = -1, nsfd_host = -1, nsfd_shmounts = -1;
	FILE *mounts;
	struct mntent *mountentry;
	char path[PATH_MAX];

	// Get a reference to current mtnns
	nsfd_current = open("/proc/self/ns/mnt", O_RDONLY);
	if (nsfd_current < 0) {
		fprintf(stderr, "Failed to open the current mntns: %s\n", strerror(errno));
		return -1;
	}

	// Attach to PID1 mntns
	nsfd_host = open("/proc/1/ns/mnt", O_RDONLY);
	if (nsfd_host < 0) {
		fprintf(stderr, "Failed to open the host mntns: %s\n", strerror(errno));
		return -1;
	}

	if (setns(nsfd_host, CLONE_NEWNS) < 0) {
		fprintf(stderr, "Failed to attach to the host mntns: %s\n", strerror(errno));
		return -1;
	}

	// Attempt to attach to our hidden mntns
	nsfd_shmounts = open("/var/snap/lxd/common/ns/shmounts", O_RDONLY);
	if (nsfd_shmounts >= 0) {
		if (setns(nsfd_shmounts, CLONE_NEWNS) == 0) {
			setup = false;
		}
	}

	// Run setup if needed
	if (setup) {
		if (setup_ns() < 0) {
			fprintf(stderr, "Failed to setup the shmounts namespace: %s\n", strerror(errno));
			return -1;
		}

		// Attach to the new hidden mntns
		nsfd_shmounts = open("/var/snap/lxd/common/ns/shmounts", O_RDONLY);
		if (nsfd_shmounts < 0) {
			fprintf(stderr, "Failed to open the shmounts mntns: %s\n", strerror(errno));
			return -1;
		}

		if (setns(nsfd_shmounts, CLONE_NEWNS) < 0) {
			fprintf(stderr, "Failed to attach to the shmounts mntns: %s\n", strerror(errno));
			return -1;
		}

		setup = true;
	}

	// Look for /run/media
	if (access("/run/media", X_OK) == 0) {
		run_media = true;
	}

	// Create temporary mountpoint
	if (run_media) {
		if (mkdir("/run/media/.lxd-shmounts", 0700) < 0 && errno != EEXIST) {
			fprintf(stderr, "Failed to create /run/media/.lxd-shmounts: %s\n", strerror(errno));
			return -1;
		}
	} else {
		if (mkdir("/media/.lxd-shmounts", 0700) < 0 && errno != EEXIST) {
			fprintf(stderr, "Failed to create /media/.lxd-shmounts: %s\n", strerror(errno));
			return -1;
		}
	}

	// Bind-mount onto temporary mountpoint
	if (run_media) {
		if (mount("/var/snap/lxd/common/shmounts", "/run/media/.lxd-shmounts", NULL, MS_BIND|MS_REC, NULL) < 0) {
			fprintf(stderr, "Failed to bind-mount /var/snap/lxd/common/shmounts to /run/media/.lxd-shmounts: %s\n", strerror(errno));
			return -1;
		}
	} else {
		if (mount("/var/snap/lxd/common/shmounts", "/media/.lxd-shmounts", NULL, MS_BIND|MS_REC, NULL) < 0) {
			fprintf(stderr, "Failed to bind-mount /var/snap/lxd/common/shmounts to /media/.lxd-shmounts: %s\n", strerror(errno));
			return -1;
		}
	}

	// Attach to the snapd mntns
	if (setns(nsfd_current, CLONE_NEWNS) < 0) {
		fprintf(stderr, "Failed to attach to the current mntns: %s\n", strerror(errno));
		return -1;
	}

	// Bind-mount onto final destination
	if (mount("/media/.lxd-shmounts", "/var/snap/lxd/common/shmounts", NULL, MS_BIND|MS_REC, NULL) < 0) {
		fprintf(stderr, "Failed to bind-mount /media/.lxd-shmounts to /var/snap/lxd/common/shmounts: %s\n", strerror(errno));
		return -1;
	}

	// Mark temporary mountpoint private
	if (mount("none", "/media/.lxd-shmounts", NULL, MS_REC|MS_PRIVATE, NULL) < 0) {
		fprintf(stderr, "Failed to mark /media/.lxd-shmounts as private: %s\n", strerror(errno));
		return -1;
	}

	// Get rid of the temporary mountpoint from snapd mntns
	if (umount2("/media/.lxd-shmounts", MNT_DETACH) < 0) {
		fprintf(stderr, "Failed to unmount /media/.lxd-shmounts: %s\n", strerror(errno));
		return -1;
	}

	// Attach to the snapd mntns
	if (setns(nsfd_host, CLONE_NEWNS) < 0) {
		fprintf(stderr, "Failed to attach to the host mntns: %s\n", strerror(errno));
		return -1;
	}

	// Attempt to cleanup mount there too (may be gone or may be there)
	if (run_media) {
		mount("none", "/run/media/.lxd-shmounts", NULL, MS_REC|MS_PRIVATE, NULL);
		umount2("/run/media/.lxd-shmounts", MNT_DETACH);
	} else {
		mount("none", "/media/.lxd-shmounts", NULL, MS_REC|MS_PRIVATE, NULL);
		umount2("/media/.lxd-shmounts", MNT_DETACH);
	}

	// Attempt to remove the temporary mountpoint
	if (run_media) {
		rmdir("/run/media/.lxd-shmounts");
	} else {
		rmdir("/media/.lxd-shmounts");
	}

	// Attempt to attach to previous LXD mntns
	nsfd_old = open("/var/snap/lxd/common/ns/mntns", O_RDONLY);
	if (nsfd_old >= 0) {
		// Attach to old ns
		if (setns(nsfd_old, CLONE_NEWNS) < 0) {
			fprintf(stderr, "Failed to attach to the old mntns: %s\n", strerror(errno));
			return -1;
		}

		// Move all the mounts we care about to shmounts
		mounts = setmntent("/proc/mounts", "r");
		if (mounts == NULL) {
			fprintf(stderr, "Failed to parse /proc/mounts: %s\n", strerror(errno));
			return -1;
		}

		// Try to undo MS_SHARED parent (not always present so ignore error)
		mount("none", "/var/snap/lxd/common/lxd/storage-pools/", NULL, MS_REC|MS_PRIVATE, NULL);

		while ((mountentry = getmntent(mounts)) != NULL) {
			if (strncmp("/var/snap/lxd/common/lxd/storage-pools/", mountentry->mnt_dir, 39) != 0) {
				continue;
			}

			if (strcmp("/var/snap/lxd/common/lxd/storage-pools/", mountentry->mnt_dir) == 0) {
				continue;
			}

			if (snprintf(path, PATH_MAX, "/var/snap/lxd/common/shmounts/storage-pools/%s", mountentry->mnt_dir + 39) < 0) {
				fprintf(stderr, "Failed to assemble mount path '%s': %s\n", path, strerror(errno));
				continue;
			}

			if (mkdir_p(path, 0700) < 0) {
				fprintf(stderr, "Failed to create mount path '%s': %s\n", path, strerror(errno));
				continue;
			}

			if (mount(mountentry->mnt_dir, path, "", MS_REC|MS_MOVE, NULL) < 0) {
				fprintf(stderr, "Failed to move mount '%s' to '%s: %s\n", mountentry->mnt_dir, path, strerror(errno));
				continue;
			}
		}

		if (endmntent(mounts) < 0) {
			fprintf(stderr, "Failed endmntent call: %s\n", strerror(errno));
			return -1;
		}

		// Attach to current ns
		if (setns(nsfd_current, CLONE_NEWNS) < 0) {
			fprintf(stderr, "Failed to attach to the current mntns: %s\n", strerror(errno));
			return -1;
		}

		// Try to make the path MS_SHARED again
		mount("none", "/var/snap/lxd/common/lxd/storage-pools/", NULL, MS_REC|MS_SHARED, NULL);

		// Move all the mounts into place
		mounts = setmntent("/proc/mounts", "r");
		if (mounts == NULL) {
			fprintf(stderr, "Failed to parse /proc/mounts: %s\n", strerror(errno));
			return -1;
		}

		while ((mountentry = getmntent(mounts)) != NULL) {
			if (strncmp("/var/snap/lxd/common/shmounts/storage-pools/", mountentry->mnt_dir, 44) != 0) {
				continue;
			}

			if (strcmp("/var/snap/lxd/common/shmounts/storage-pools/", mountentry->mnt_dir) == 0) {
				continue;
			}

			if (snprintf(path, PATH_MAX, "/var/snap/lxd/common/lxd/storage-pools/%s", mountentry->mnt_dir + 44) < 0) {
				fprintf(stderr, "Failed to assemble mount path '%s': %s\n", path, strerror(errno));
				continue;
			}

			if (mkdir_p(path, 0700) < 0) {
				fprintf(stderr, "Failed to create mount path '%s': %s\n", path, strerror(errno));
				continue;
			}

			if (mount(mountentry->mnt_dir, path, "", MS_REC|MS_BIND, NULL) < 0) {
				fprintf(stderr, "Failed to bind-mount '%s' onto '%s: %s\n", mountentry->mnt_dir, path, strerror(errno));
				continue;
			}

			if (umount2(mountentry->mnt_dir, MNT_DETACH) < 0) {
				fprintf(stderr, "Failed to umount '%s': %s\n", mountentry->mnt_dir, strerror(errno));
				continue;
			}
		}

		if (endmntent(mounts) < 0) {
			fprintf(stderr, "Failed endmntent call: %s\n", strerror(errno));
			return -1;
		}

		// Attach back to host ns
		if (setns(nsfd_host, CLONE_NEWNS) < 0) {
			fprintf(stderr, "Failed to setns back to hostns: %s\n", strerror(errno));
			return -1;
		}

		// Get rid of the mount, we're done here
		if (umount2("/var/snap/lxd/common/ns/mntns", MNT_DETACH) < 0) {
			fprintf(stderr, "Failed to unmount old mntns: %s\n", strerror(errno));
			return -1;
		}
	}

	// Save our current mntns
	fd = open("/var/snap/lxd/common/ns/mntns", O_CREAT | O_RDWR, 0600);
	if (fd < 0) {
		fprintf(stderr, "Failed to create new mntns mountpoint: %s\n", strerror(errno));
		return -1;
	}
	close(fd);

	// Make sure that the ns path is still private
	if (mount("none", "/var/snap/lxd/common/ns", NULL, MS_PRIVATE, NULL) < 0) {
		fprintf(stderr, "Failed to mark mount as private: %s\n", strerror(errno));
		return -1;
	}

	if (mount("/run/snapd/ns/lxd.mnt", "/var/snap/lxd/common/ns/mntns", NULL, MS_BIND, NULL) < 0) {
		fprintf(stderr, "Failed to mount new mntns: %s\n", strerror(errno));
		return -1;
	}

	// Close open fds
	close(nsfd_host);
	close(nsfd_old);
	close(nsfd_current);
	close(nsfd_shmounts);
	return 0;
}
