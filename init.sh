#!/sbin/dinit /bin/sh

uid=$(id -u)

if [[ ${uid} -eq 0 ]]; then
    echo "init container"

    # set container's time zone
    cp /usr/share/zoneinfo/${TIME_ZONE} /etc/localtime
    echo "${TIME_ZONE}" >/etc/timezone
    echo "set timezone ${TIME_ZONE} ($(date))"
fi

echo "execute \"$@\""
exec $@
