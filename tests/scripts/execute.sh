#!/bin/bash

# Copyright 2019 The KubeEdge Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e

workdir=`pwd`
cd $workdir

curpath=$PWD
echo $PWD

GOPATH=${GOPATH:-$(go env GOPATH)}

check_ginkgo_v2() {
    # check if ginkgo is installed
    which ginkgo &> /dev/null || (
        echo "ginkgo is not found, install first"
        go install github.com/onsi/ginkgo/v2/ginkgo@v2.1.4
        sudo cp $GOPATH/bin/ginkgo /usr/local/bin/
        return
    )
    
    # check if the ginkgo version is v2
    local -a ginkgo_version_output
    read -ra ginkgo_version_output <<< $(ginkgo version)
    # Assuming ginkgo version output format is: 
    # Ginkgo Version 2.1.4
    if [[ "${ginkgo_version_output[2]}" != "2.1.4" ]]; then
        echo "ginkgo version is not v2.1.4, reinstall ginkgo v2.1.4"
        go install github.com/onsi/ginkgo/v2/ginkgo@v2.1.4
        sudo cp $GOPATH/bin/ginkgo /usr/local/bin/
    fi
}

cleanup() {
    bash ${curpath}/tests/scripts/cleanup.sh
}

check_ginkgo_v2
cleanup

E2E_DIR=${curpath}/tests/e2e
sudo rm -rf ${E2E_DIR}/deployment/deployment.test
sudo rm -rf ${E2E_DIR}/device_crd/device_crd.test

# Specify the module name to compile in below command
bash -x ${curpath}/tests/scripts/compile.sh $1

ENABLE_DAEMON=true bash -x ${curpath}/hack/local-up-kubeedge.sh || {
    echo "failed to start cluster !!!"
    exit 1
}

kubectl create clusterrolebinding system:anonymous --clusterrole=cluster-admin --user=system:anonymous

:> /tmp/testcase.log

export GINKGO_TESTING_RESULT=0

trap cleanup EXIT

bash -x ${curpath}/tests/scripts/fast_test.sh $1
