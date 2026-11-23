ALTER TABLE user_photos
ADD CONSTRAINT user_photos_photo_type_check
CHECK (photo_type IN ('portrait', 'reference'));