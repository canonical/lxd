keep_running="yes"

trap 'keep_running="no"' 1

while [ $keep_running == "yes" ]; do
 # main body of your script here
 sleep 0
done

echo "Called with signal "

keep_running="yes"

trap 'keep_running="no"' 10

while [ $keep_running == "yes" ]; do
  sleep 0
done

echo "Called with signal 10"

exit 1