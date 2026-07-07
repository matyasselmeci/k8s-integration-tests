#!/bin/bash
Prefix=${1:-~/pelican-test-$(date +%F_%H.%M.%S)/testfiles}
Min=0
Max=31

Blocksize=1000000
NumBlocks=48

fail () {
        local ret=$1
        shift
        echo >&2 "$@"
        exit "$ret"
}

mkdir -p "$Prefix" || fail 3 "Could not create destination dir $Prefix"
cd "$Prefix" || fail 4 "Could not enter destination dir $Prefix"

# If there's a testfiles_range envvar, use that to define the range of testfiles, otherwise use Min and Max
if [ ! -z ${testfiles_range+x} ] ; then
   create_range=${testfiles_range}
else
    create_range=$(seq -w ${Min} ${Max})
fi

for it in ${create_range}
do
        File=test${it}.b64
        Md5File=${File}.md5
        dd if=/dev/urandom count=$NumBlocks bs=$Blocksize | base64 > "$File" && \
                md5sum "$File" | tee "$Md5File"
        [[ -s $File ]] || fail 5 "File $File missing or has 0 length"
        [[ -s $Md5File ]] || fail 6 "md5sum file $Md5File missing or has 0 length"
        md5sum -c "$Md5File" || fail 7 "Checksum for $File failed"
done

