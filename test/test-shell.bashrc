# lxc command completion
if [ -f /etc/profile.d/bash_completion.sh ] && command -v lxc > /dev/null; then
    . /etc/profile.d/bash_completion.sh
    . <(lxc completion bash)
fi

# provide useful aliases like `ll`, etc
if [ -f ~/.bashrc ]; then
    . ~/.bashrc
fi
