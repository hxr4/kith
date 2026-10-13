"""Download MNIST (stdlib only) and pack it as dataset.zip for the Kith demo.

Run once on the requester:  python3 fetch_mnist.py
"""
import os
import urllib.request
import zipfile

MIRROR = "https://ossci-datasets.s3.amazonaws.com/mnist/"
FILES = ["train-images-idx3-ubyte.gz", "train-labels-idx1-ubyte.gz",
         "t10k-images-idx3-ubyte.gz", "t10k-labels-idx1-ubyte.gz"]

here = os.path.dirname(os.path.abspath(__file__))
with zipfile.ZipFile(os.path.join(here, "dataset.zip"), "w", zipfile.ZIP_STORED) as z:
    for f in FILES:
        print("downloading", f)
        z.writestr(f, urllib.request.urlopen(MIRROR + f).read())
print("wrote dataset.zip")
