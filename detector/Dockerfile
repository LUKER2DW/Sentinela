# syntax=docker/dockerfile:1
# Object detector sidecar (ADR-0016). CPU only, one thread; the compose
# file caps it at 15 % of one core and 192 MB.

# 1. Export YOLO26n to ONNX. Ultralytics + PyTorch exist only in this stage;
#    the weights are AGPL-3.0 (ADR-0016) and are never committed.
FROM python:3.12-slim@sha256:f77ac9e44ae96ef2c90b8053ea08c31f8be030f824196b0ae4db6d462c84e51f AS export
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends libgl1 libglib2.0-0 libxcb1 >/dev/null \
 && pip install --no-cache-dir --root-user-action ignore --extra-index-url https://download.pytorch.org/whl/cpu \
      torch==2.14.0+cpu ultralytics==8.4.165 onnx onnxslim onnxruntime==1.23.2
ADD --checksum=sha256:9b09cc8bf347f0fc8a5f7657480587f25db09b34bf33b0652110fb03a8ad4fef \
    https://github.com/ultralytics/assets/releases/download/v8.4.0/yolo26n.pt /export/yolo26n.pt
RUN cd /export && YOLO_OFFLINE=1 python -c "from ultralytics import YOLO; YOLO('yolo26n.pt').export(format='onnx', imgsz=416, opset=17, simplify=True, dynamic=False)" \
 && python -c "import onnxruntime as o; s = o.InferenceSession('/export/yolo26n.onnx'); assert s.get_outputs()[0].shape == [1, 84, 3549], s.get_outputs()[0].shape"

# 2. Runtime: ONNX Runtime + PyAV (bundled FFmpeg for RTSP/HEVC) + numpy.
FROM python:3.12-slim@sha256:f77ac9e44ae96ef2c90b8053ea08c31f8be030f824196b0ae4db6d462c84e51f
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 OMP_NUM_THREADS=1
# sympy/mpmath are only used by onnxruntime's optional tooling, not inference.
RUN pip install --no-cache-dir --root-user-action ignore onnxruntime==1.23.2 numpy==2.5.3 av==18.1.0 \
 && pip uninstall -y -q --root-user-action ignore sympy mpmath \
 && find /usr/local/lib/python3.12 -name tests -type d -prune -exec rm -rf {} + \
 && python -c "import onnxruntime, av, numpy"
COPY --from=export /export/yolo26n.onnx /opt/detector/model.onnx
COPY detector.py test_detector.py /opt/detector/
RUN chmod 0644 /opt/detector/* && cd /opt/detector && python -m unittest -q test_detector && rm test_detector.py
USER 1000:1000
CMD ["python", "/opt/detector/detector.py"]
