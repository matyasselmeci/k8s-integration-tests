#
# This file is for reference only:
# These are the current integration tests that are run on the ITB federation
# and need to be converted to Terratest.
#
#
# debugging and fail on errors
#set -euxo pipefail
#set -ux
# Set up variables

export PELICAN_TRANSPORT_RESPONSEHEADERTIMEOUT=30s
export PELICAN_CLIENT_WORKERCOUNT=20
export PELICAN_CLIENT_PREFERREDCACHES="https://itb-osdf-pelican-cache.osdf-dev.chtc.io:8443 https://itb-osdf-pelican-cache.osdf-dev.chtc.io:8443 https://itb-osdf-pelican-cache.osdf-dev.chtc.io:8443"

#export PELICAN_CLIENT_PREFERREDCACHES="https://itb-helm-cache.osdf-dev.chtc.io:8443 https://itb-helm-cache.osdf-dev.chtc.io:8443 https://itb-helm-cache.osdf-dev.chtc.io:8443"

top_level_dir=~/pelican-itb-tests
test_run_dir=/pelican-test-$(date +%F_%H.%M.%S)
testfiles_dir=${top_level_dir}${test_run_dir}/testfiles
backfiles_dir=${top_level_dir}${test_run_dir}/backfiles
logs_dir=${top_level_dir}${test_run_dir}/logs
token_file=${top_level_dir}${test_run_dir}/ITB-OSDF-pelican-origin-token
echo "test dir is ${top_level_dir}${test_run_dir}"

mkdir -p ${logs_dir}

export testfiles_range=$(seq -w 0 25)

# Get token from origin for writes and private reads/lists
#kubectl -n osdf-dev exec deployment/pelicanplatform-osdf-pelican-origin -- pelican-server origin token create --audience "https://wlcg.cern.ch/jwt/v1/any" --issuer "https://pelicanplatform-origin.osdf-dev.chtc.io" --scope "storage.read:/ storage.modify:/ storage.create:/" --subject "anything" --lifetime 36000 > "${token_file}"

kubectl -n osdf-dev exec deployment/pelicanplatform-osdf-pelican-origin -- osdf-server origin token create --audience "https://wlcg.cern.ch/jwt/v1/any" --issuer "https://pelicanplatform-origin.osdf-dev.chtc.io" --scope "storage.read:/ storage.modify:/ storage.create:/" --subject "anything" --lifetime 36000 > "${token_file}"

#test for size greater than zero
if [ ! -s ${token_file} ] ;
then
    exit 1
fi

#Make testfiles, test files show up in ~/test_run_dir/testfiles as test{0-30}.b64
./make_testfiles.sh "${testfiles_dir}"

#Listings Checks
pelican object ls -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/pelicanplatform -L "${logs_dir}"/pelicanplatform-list-test.log >> "${logs_dir}"/pelicanplatform-list-test.log

pelican object ls -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test -L "${logs_dir}"/writes-list-test.log >> "${logs_dir}"/writes-list-test.log

pelican object ls -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/no-listings -L "${logs_dir}"/no-listings-list-test.log >> "${logs_dir}"/no-listings-list-test.log && exit 1

#Read Checks directories
mkdir -p "${backfiles_dir}"/read-tests-pelicanplatform/
mkdir -p "${logs_dir}"/read-tests-pelicanplatform/

#Initial read checks
for object in $(pelican object ls pelican://osdf-itb.osg-htc.org/pelicanplatform | grep -vE "\.sif") ; do 
    pelican object get -d pelican://osdf-itb.osg-htc.org/pelicanplatform/"${object}" "${backfiles_dir}"/read-tests-pelicanplatform/miss-"${object}" -L "${logs_dir}"/read-tests-pelicanplatform/reads-test-cache-miss-"${object}".log
done

for object in $(pelican object ls pelican://osdf-itb.osg-htc.org/pelicanplatform | grep -vE "\.sif") ; do 
    pelican object get -d pelican://osdf-itb.osg-htc.org/pelicanplatform/"${object}" "${backfiles_dir}"/read-tests-pelicanplatform/hit-"${object}" -L "${logs_dir}"/read-tests-pelicanplatform/reads-test-cache-hit-"${object}".log
done


wait
md5sum "${backfiles_dir}"/read-tests-pelicanplatform/* | awk -F"/|-" '{print $1 $NF}' | sort -k2 | uniq -c


# Non-exist read checks, this should fail
pelican object get -d pelican://osdf-itb.osg-htc.org/pelicanplatform/non-exist.txt "${backfiles_dir}"/read-tests-pelicanplatform/non-exist.txt -L "${logs_dir}"/read-tests-pelicanplatform/non-exist.log -- && exit 1

# Direct Read of non-existant file check, this should fail
pelican object get -d pelican://osdf-itb.osg-htc.org/pelicanplatform/non-exist-direct.txt?directread "${backfiles_dir}"/read-tests-pelicanplatform/non-exist-direct.txt -L "${logs_dir}"/read-tests-pelicanplatform/non-exist-direct.log -- && exit 1

# Writes Checks directories
mkdir -p "${logs_dir}"/write-tests/
mkdir -p "${logs_dir}"/write-tests-pelicanplatform/
mkdir -p "${logs_dir}"/write-tests-readback/

echo "Doing writes"

# Writes Checks for the /chtc/itb/write-test namespace
for index in ${testfiles_range}; do pelican object put -d -t "${token_file}" "${testfiles_dir}"/test"${index}".b64 pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test/"${test_run_dir}"/test"${index}".b64 -L "${logs_dir}"/write-tests/writes-test-"${index}".log ; done

# Writes Checks for the /pelicanplatform namespace, these should fail since that namespace had writes disabled
for index in ${testfiles_range}; do pelican object put -d -t "${token_file}" "${testfiles_dir}"/test"${index}".b64 pelican://osdf-itb.osg-htc.org/pelicanplatform/"${test_run_dir}"/test"${index}".b64 -L "${logs_dir}"/write-tests-pelicanplatform/writes-test-"${index}".log  ; done

# Checks for reading the written files
for object in $( pelican object ls -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}" ) ; do pelican object get -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}"/"${object}" "${backfiles_dir}"/writes-test/miss-"${object}" -L "${logs_dir}"/write-tests-readback/reads-test-cache-miss-"${object}".log  ; done

for object in $( pelican object ls -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}" ) ; do pelican object get -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}"/"${object}" "${backfiles_dir}"/writes-test/hit-"${object}" -L "${logs_dir}"/write-tests-readback/reads-test-cache-hit-"${object}".log ; done

# Direct Read check
for object in $( pelican object ls -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}" ) ; do pelican object get -d -t "${token_file}" pelican://osdf-itb.osg-htc.org/chtc/itb/writes-test"${test_run_dir}"/"${object}"?directread "${backfiles_dir}"/writes-test/direct-"${object}" -L "${logs_dir}"/write-tests-readback/reads-test-direct-"${object}".log ; done

# Verify
md5sum "${backfiles_dir}"/writes-test/*.b64 "${testfiles_dir}"/*.b64 | awk -F"/|-" '{print $1 $NF}' | sort -k2 | uniq -c
