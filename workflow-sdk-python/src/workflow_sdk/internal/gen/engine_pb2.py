import struct

class JobDispatchEvent:
    def __init__(self):
        self.schema_version = 0
        self.dedup_id = ""
        self.job_id = ""
        self.instance_id = ""
        self.step_execution_id = ""
        self.job_type = ""
        self.retries_remaining = 0
        self.job_payload = b""
        # self.created_at skipped for now

    def ParseFromString(self, data: bytes):
        pos = 0
        while pos < len(data):
            tag_wire = self._read_varint(data, pos)
            pos = tag_wire[1]
            tag = tag_wire[0] >> 3
            wire = tag_wire[0] & 7
            
            if wire == 0: # Varint
                val, next_pos = self._read_varint(data, pos)
                if tag == 1: self.schema_version = val
                elif tag == 20: self.retries_remaining = val
                pos = next_pos
            elif wire == 2: # Length-delimited
                length, next_pos = self._read_varint(data, pos)
                pos = next_pos
                val = data[pos:pos+length]
                if tag == 2: self.dedup_id = val.decode('utf-8')
                elif tag == 10: self.job_id = val.decode('utf-8')
                elif tag == 11: self.instance_id = val.decode('utf-8')
                elif tag == 12: self.step_execution_id = val.decode('utf-8')
                elif tag == 13: self.job_type = val.decode('utf-8')
                elif tag == 30: self.job_payload = val
                pos += length
            else:
                # Skip other types
                if wire == 1: pos += 8
                elif wire == 5: pos += 4
                else: break # Should not happen in this simple schema

    def _read_varint(self, data, pos):
        result = 0
        shift = 0
        while True:
            b = data[pos]
            result |= (b & 0x7f) << shift
            pos += 1
            if not (b & 0x80):
                return result, pos
            shift += 7
